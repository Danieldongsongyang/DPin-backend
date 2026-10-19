#!/usr/bin/env python3
"""只读核对当前用户的最新本机职位来源、服务端映射与真实飞书字段。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('--queue', required=True)
    parser.add_argument('--user-id', required=True, type=int)
    parser.add_argument('--evidence-dir', required=True)
    args = parser.parse_args()
    config = json.loads(Path(args.config).read_text())
    queue = json.loads(Path(args.queue).read_text())
    evidence = Path(args.evidence_dir)
    evidence.mkdir(mode=0o700, parents=True, exist_ok=True)
    latest = {}
    for item in queue['items']:
        if item['owner']['user_id'] != args.user_id or item['source_error']:
            continue
        assert item['owner']['api_origin'] == 'http://localhost:3000'
        for job in item['body']['jobs']:
            key = (item['account_key'], job['job_id'])
            if key not in latest or item['body']['observed_at'] > latest[key][0]['body']['observed_at']:
                latest[key] = (item, job)
    assert latest, 'NO_VERIFIED_SOURCE'
    env = {'PATH': '/usr/bin:/bin', 'HOME': config['data_dir'] + '/cli-home', 'LARKSUITE_CLI_CONFIG_DIR': config['data_dir'] + '/cli-home',
           'LARKSUITE_CLI_APP_ID': config['app_id'], 'LARKSUITE_CLI_APP_SECRET': json.loads(Path(config['secret_file']).read_text())['app_secret'],
           'LARKSUITE_CLI_BRAND': 'feishu', 'LARKSUITE_CLI_DEFAULT_AS': 'bot', 'LARKSUITE_CLI_STRICT_MODE': 'bot',
           'LARKSUITE_CLI_NO_UPDATE_NOTIFIER': '1', 'LARKSUITE_CLI_NO_SKILLS_NOTIFIER': '1'}
    database = Path(__file__).resolve().parents[2] / 'one-api.db'
    result = {'ok': False, 'read_only': True, 'user_id': args.user_id, 'jobs': []}
    try:
        for (account_key, job_id), (item, source) in latest.items():
            with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
                db.row_factory = sqlite3.Row
                row = db.execute('SELECT c.*,m.record_id,a.account_key,a.display_name,u.username FROM bello_buddy_job_catalogs c JOIN bello_buddy_resume_job_maps m ON m.id=c.job_map_id JOIN bello_buddy_accounts a ON a.id=c.account_id JOIN users u ON u.id=c.user_id WHERE c.user_id=? AND a.account_key=? AND c.platform_job_id=?', (args.user_id, account_key, job_id)).fetchone()
                assert row and row['state'] == 'synced' and row['observed_at'] == item['body']['observed_at'], 'SOURCE_NOT_YET_SYNCED'
                assert db.execute('SELECT COUNT(*) FROM bello_buddy_resume_job_maps WHERE id=?', (row['job_map_id'],)).fetchone()[0] == 1
            process = subprocess.run([config['cli_path'], 'base', '+record-get', '--base-token', config['target']['base'], '--table-id', config['target']['job_table'], '--record-id', row['record_id'], '--as', 'bot', '--format', 'json'], env=env, cwd=config['data_dir'], capture_output=True, timeout=50)
            assert process.returncode == 0, 'CLI_READ_FAILED'
            value = json.loads(process.stdout)
            assert value['ok'] is True and value['identity'] == 'bot'
            data = value['data']
            assert data['record_id_list'] == [row['record_id']]
            actual = dict(zip(data['field_id_list'], data['data'][0]))
            target, fields = config['target']['fields'], config['job_sync']['fields']
            expected = {target['job_name']: source['name'], target['hr']: row['username'], fields['account_id']: account_key,
                        fields['account']: row['display_name'], fields['job_id']: job_id, fields['platform']: 'BOSS直聘', fields['online']: '在招' if source['online'] else '已下线'}
            if 'city' in source:
                expected[fields['city']] = source['city']
            if 'boss_description' in source:
                expected[fields['boss_description']] = source['boss_description']
            assert all(actual.get(key) == value for key, value in expected.items()), 'FIELD_READBACK_MISMATCH'
            jd = source.get('boss_description', '')
            result['jobs'].append({'job_id': job_id, 'record_id': row['record_id'], 'client_sync_id': item['body']['client_sync_id'],
                                   'state': row['state'], 'warning': row['warning'], 'fields_verified': len(expected),
                                   'jd_chars': len(jd), 'jd_sha256': hashlib.sha256(jd.encode()).hexdigest(), 'mapping_count': 1})
        result['ok'] = True
    finally:
        filename = evidence / ('live-job-readback-' + uuid.uuid4().hex + '.json')
        fd = os.open(filename, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'w') as file:
            json.dump(result, file, ensure_ascii=False, indent=2)
        print(json.dumps(result | {'evidence': str(filename)}, ensure_ascii=False))


if __name__ == '__main__':
    main()
