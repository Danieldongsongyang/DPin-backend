#!/usr/bin/env python3
"""仅使用明确的 F1_TEST 对象，验证真实 D-PIN / 固定 CLI / 飞书职位同步。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--config', required=True)
    parser.add_argument('--credentials-file', required=True)
    parser.add_argument('--evidence-dir', required=True)
    parser.add_argument('--resume-only', action='store_true')
    args = parser.parse_args()
    config = json.loads(Path(args.config).read_text())
    evidence = Path(args.evidence_dir)
    evidence.mkdir(mode=0o700, parents=True, exist_ok=True)
    state_path = evidence / 'state.json'
    state = json.loads(state_path.read_text()) if state_path.exists() else {'run_id': uuid.uuid4().hex[:12], 'intents': {}, 'checks': {}}

    def save():
        temporary = state_path.with_suffix('.tmp')
        fd = os.open(temporary, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'w') as file:
            json.dump(state, file, ensure_ascii=False, indent=2)
            file.flush()
            os.fsync(file.fileno())
        os.replace(temporary, state_path)

    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def api(method, route, token='', body=None, content_type='application/json', expected=(200, 202)):
        data = body if isinstance(body, bytes) else json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request('http://127.0.0.1:3000' + route, method=method, data=data,
                                        headers={'Authorization': 'Bearer ' + token, 'Content-Type': content_type})
        try:
            response = opener.open(request, timeout=60)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            status, reply = response.code, json.load(response)
        assert status in (expected if isinstance(expected, tuple) else (expected,)), ('API_STATUS', status, reply.get('code'))
        if status not in (200, 202):
            return reply
        assert reply.get('success') is True, reply.get('code')
        return reply['data']

    tokens = {}
    for user in json.loads(Path(args.credentials_file).read_text()):
        assert user['username'] in ('F1_TEST', 'F1_TEST_B')
        tokens[user['username']] = api('POST', '/api/user/login', body=user)['access_token']
    env = {'PATH': '/usr/bin:/bin', 'HOME': config['data_dir'] + '/cli-home',
           'LARKSUITE_CLI_CONFIG_DIR': config['data_dir'] + '/cli-home', 'LARKSUITE_CLI_APP_ID': config['app_id'],
           'LARKSUITE_CLI_APP_SECRET': json.loads(Path(config['secret_file']).read_text())['app_secret'],
           'LARKSUITE_CLI_BRAND': 'feishu', 'LARKSUITE_CLI_DEFAULT_AS': 'bot', 'LARKSUITE_CLI_STRICT_MODE': 'bot',
           'LARKSUITE_CLI_NO_UPDATE_NOTIFIER': '1', 'LARKSUITE_CLI_NO_SKILLS_NOTIFIER': '1', 'TZ': 'Asia/Shanghai'}

    def cli(command, table, flags):
        process = subprocess.run([config['cli_path'], 'base', command, '--base-token', config['target']['base'],
                                  '--table-id', table, '--as', 'bot', '--format', 'json'] + flags,
                                 cwd=config['data_dir'], env=env, capture_output=True, timeout=60)
        if process.returncode:
            failure_path = evidence / ('cli-read-failure-' + uuid.uuid4().hex + '.json')
            fd = os.open(failure_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'w') as output:
                json.dump({'command': command, 'exit_code': process.returncode, 'stdout': process.stdout.decode(errors='replace'), 'stderr': process.stderr.decode(errors='replace')}, output)
            raise RuntimeError('CLI_RESULT_UNKNOWN')
        value = json.loads(process.stdout)
        assert value.get('ok') is True and value.get('identity') == 'bot'
        return value['data']

    def record(table, record_id):
        value = cli('+record-get', table, ['--record-id', record_id])
        assert value['record_id_list'] == [record_id]
        return dict(zip(value['field_id_list'], value['data'][0]))

    database = Path(__file__).resolve().parents[2] / 'one-api.db'

    def map_id(client_id):
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            db.row_factory = sqlite3.Row
            batch = db.execute('SELECT catalog_ids FROM bello_buddy_job_sync_batches WHERE client_sync_id=?', (client_id,)).fetchone()
            ids = json.loads(batch['catalog_ids'])
            row = db.execute('SELECT c.*,m.record_id FROM bello_buddy_job_catalogs c JOIN bello_buddy_resume_job_maps m ON m.id=c.job_map_id WHERE c.id=?', (ids[0],)).fetchone()
            assert row['platform_job_id'].startswith('F1_TEST')
            return dict(row)

    account_a = 'F1_TEST_JOBSYNC_A_' + state['run_id']
    account_b = 'F1_TEST_JOBSYNC_B_' + state['run_id']
    job_id = 'F1_TEST_JOBSYNC_JOB_' + state['run_id']
    name_field, hr_field = config['target']['fields']['job_name'], config['target']['fields']['hr']
    fields = config['job_sync']['fields']
    initial = {'job_id': job_id, 'name': 'F1_TEST 职位同步同名岗位', 'online': True, 'city': 'F1_TEST_CITY', 'boss_description': 'F1_TEST 完整JD\n职责与要求，不含个人信息'}

    def submit(phase, token, account, jobs, trigger='list'):
        base = '/api/bellobuddy/accounts/' + account + '/job-catalog/syncs'
        if phase not in state['intents']:
            state['intents'][phase] = {'client_sync_id': 'F1_TEST_' + uuid.uuid4().hex, 'observed_at': int(time.time() * 1000), 'display_name': account, 'trigger': trigger, 'jobs': jobs}
            save()
        body = state['intents'][phase]
        receipt = api('POST', base, token, body)
        deadline = time.monotonic() + 180
        while any(job['state'] != 'synced' for job in receipt['jobs']):
            assert not any(job['state'] == 'blocked' for job in receipt['jobs']), ('JOB_BLOCKED', [(j.get('code'), j['state']) for j in receipt['jobs']])
            assert time.monotonic() < deadline, 'SYNC_TIMEOUT'
            time.sleep(3)
            receipt = api('GET', base + '/' + body['client_sync_id'], token)
        print(json.dumps({'phase': phase, 'state': 'synced', 'jobs': len(receipt['jobs'])}), flush=True)
        return receipt

    try:
        token = tokens['F1_TEST']
        first_id = state['intents'].get('initial', {}).get('client_sync_id')
        original_record = map_id(first_id)['record_id'] if first_id else None
        if not args.resume_only:
            first = submit('initial', token, account_a, [initial], 'login')
            first_id = first['client_sync_id']
            mapping = map_id(first_id)
            row = record(config['target']['job_table'], mapping['record_id'])
            assert row[name_field] == initial['name'] and row[hr_field] == 'F1_TEST'
            assert row[fields['boss_description']] == initial['boss_description']
            state['checks']['real_job_jd_owner_readback'] = True
            original_record = mapping['record_id']
            repeated = submit('initial', token, account_a, [initial], 'login')
            assert repeated['client_sync_id'] == first_id and map_id(first_id)['record_id'] == original_record
            state['checks']['same_id_idempotency'] = True
            wrong = dict(state['intents']['initial'], display_name='F1_TEST_CHANGED')
            assert api('POST', '/api/bellobuddy/accounts/' + account_a + '/job-catalog/syncs', token, wrong, expected=409)['code'] == 'JOB_SYNC_ID_CONFLICT'
            api('GET', '/api/bellobuddy/accounts/' + account_a + '/job-catalog/syncs/' + first_id, tokens['F1_TEST_B'], expected=404)
            state['checks']['id_conflict_and_cross_user_denied'] = True
            # 手工字段保护，只写入本次明确创建的无个人信息 F1_TEST 行；写前意图留证，不明不重发。
            if 'manual_write_started' not in state:
                state['manual_write_started'] = True
                save()
                cli('+record-upsert', config['target']['job_table'], ['--record-id', original_record, '--json', json.dumps({'fldc1Ko8AU': 'F1_TEST 手工职位描述保留', name_field: 'F1_TEST 手工改系统字段'}, ensure_ascii=False)])
                state['manual_write_confirmed'] = True
                save()
            assert state.get('manual_write_confirmed'), 'MANUAL_TEST_WRITE_UNKNOWN'
            for phase, online in [('offline', False), ('reopen', True)]:
                source = dict(initial, name='F1_TEST 职位同步改名', online=online, boss_description='F1_TEST BOSS新版完整JD\n更新职责')
                receipt = submit(phase, token, account_a, [source], 'refresh')
                mapping = map_id(receipt['client_sync_id'])
                assert mapping['record_id'] == original_record
                row = record(config['target']['job_table'], original_record)
                assert row[fields['online']] == ('在招' if online else '已下线') and row[name_field] == source['name']
                assert row[fields['boss_description']] == source['boss_description'] and row['fldc1Ko8AU'] == 'F1_TEST 手工职位描述保留'
                assert not row.get('fldjvfC7AB')
            state['checks']['rename_offline_reopen_same_row_manual_preserved'] = True
            without_jd = dict(initial, name='F1_TEST 职位同步改名')
            without_jd.pop('boss_description')
            receipt = submit('missing-jd', token, account_a, [without_jd])
            assert receipt['jobs'][0]['warning'] == 'BOSS_JD_UNAVAILABLE'
            assert record(config['target']['job_table'], original_record)[fields['boss_description']] == 'F1_TEST BOSS新版完整JD\n更新职责'
            submit('empty-observation', token, account_a, [])
            assert record(config['target']['job_table'], original_record)[fields['online']] == '在招'
            state['checks']['missing_jd_and_absence_preserve_history'] = True
            for phase, other_token, other_account, other_job in [('account-isolation', token, account_b, initial), ('user-isolation', tokens['F1_TEST_B'], account_a, initial), ('job-isolation', token, account_a, dict(initial, job_id=job_id + '_B'))]:
                receipt = submit(phase, other_token, other_account, [other_job])
                assert map_id(receipt['client_sync_id'])['record_id'] != original_record
            state['checks']['same_name_four_scope_isolation'] = True
        # 真实PDF接收及归档必须关联同一职位行。
        archive_route = '/api/bellobuddy/accounts/' + account_a + '/jobs/' + job_id + '/resume-archives'
        upload_id = 'F1_TEST_JOBSYNC_UPLOAD_' + state['run_id']
        prior = api('GET', archive_route + '/by-upload/' + upload_id, token, expected=(200, 404))
        if prior.get('archive_id'):
            state['archive_id'] = prior['archive_id']; save()
        if not state.get('archive_id'):
            pdf = (Path(args.credentials_file).parent / 'F1_TEST.pdf').read_bytes()
            boundary = 'F1_TEST_' + uuid.uuid4().hex
            metadata = {'client_upload_id': upload_id, 'candidate_id': 'F1_TEST_JOBSYNC_CANDIDATE', 'downloaded_at': state['intents']['initial']['observed_at']}
            multipart = (f'--{boundary}\r\nContent-Disposition: form-data; name="metadata"\r\n\r\n'.encode() + json.dumps(metadata).encode() + f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="F1_TEST.pdf"\r\nContent-Type: application/pdf\r\n\r\n'.encode() + pdf + f'\r\n--{boundary}--\r\n'.encode())
            archive = api('POST', archive_route, token, multipart, 'multipart/form-data; boundary=' + boundary)
            state['archive_id'] = archive['archive_id']; save()
        deadline = time.monotonic() + 180
        reconciled = False
        while True:
            archive = api('GET', '/api/bellobuddy/resume-archives/' + state['archive_id'], token)
            if archive['state'] == 'archived': break
            if archive['state'] == 'blocked' and not reconciled:
                api('POST', '/api/bellobuddy/resume-archives/' + state['archive_id'] + '/reconcile', token, {})
                reconciled = True
                time.sleep(3)
                continue
            assert archive['state'] != 'blocked', archive.get('code')
            assert time.monotonic() < deadline
            time.sleep(3)
        with sqlite3.connect('file:' + str(database) + '?mode=ro', uri=True) as db:
            talent_id, job_map_id = db.execute('SELECT s.record_id,s.job_map_id FROM bello_buddy_resume_archives a JOIN bello_buddy_resume_scopes s ON s.id=a.scope_id WHERE a.archive_id=?', (state['archive_id'],)).fetchone()
        assert job_map_id == map_id(first_id)['job_map_id']
        talent = record(config['target']['talent_table'], talent_id)
        assert talent[config['target']['fields']['job_link']] == [{'id': original_record}]
        assert archive['sha256'] == hashlib.sha256((Path(args.credentials_file).parent / 'F1_TEST.pdf').read_bytes()).hexdigest()
        state['checks']['real_resume_same_job_row_and_hash'] = True
        state['job_record'] = original_record
        state['ok'] = True
    finally:
        save()
        print(json.dumps({'ok': state.get('ok', False), 'checks': state['checks'], 'evidence': str(evidence)}, ensure_ascii=False), flush=True)


if __name__ == '__main__':
    main()
