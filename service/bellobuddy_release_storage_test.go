package service

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestBelloBuddyReleaseStorageDatabaseMatrix(t *testing.T) {
	for _, database := range []common.DatabaseType{common.DatabaseTypeSQLite, common.DatabaseTypeMySQL, common.DatabaseTypePostgreSQL} {
		t.Run(string(database), func(t *testing.T) {
			var driver gorm.Dialector
			switch database {
			case common.DatabaseTypeSQLite:
				driver = sqlite.Open("file:release-storage-" + uuid.NewString() + "?mode=memory&cache=shared")
			case common.DatabaseTypeMySQL:
				dsn := os.Getenv("DPIN_RELEASE_TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("DPIN_RELEASE_TEST_MYSQL_DSN is required for the real MySQL matrix")
				}
				driver = mysql.Open(dsn)
			case common.DatabaseTypePostgreSQL:
				dsn := os.Getenv("DPIN_RELEASE_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("DPIN_RELEASE_TEST_POSTGRES_DSN is required for the real PostgreSQL matrix")
				}
				driver = postgres.Open(dsn)
			}
			beforeMain, beforeLog := common.MainDatabaseType(), common.LogDatabaseType()
			common.SetDatabaseTypes(database, beforeLog)
			t.Cleanup(func() { common.SetDatabaseTypes(beforeMain, beforeLog) })
			base := os.Getenv("DPIN_RELEASE_TEST_STORAGE_ROOT")
			if base == "" {
				cwd, err := os.Getwd()
				require.NoError(t, err)
				base = filepath.Dir(cwd)
			}
			require.True(t, filepath.IsAbs(base), "release test storage base must be absolute")
			require.NoError(t, os.MkdirAll(base, 0700))
			storagePath, err := os.MkdirTemp(base, ".dpin-release-storage-test-")
			require.NoError(t, err)
			t.Cleanup(func() {
				resolved, err := filepath.EvalSymlinks(storagePath)
				if !assert.NoError(t, err) {
					return
				}
				relative, err := filepath.Rel(base, storagePath)
				if !assert.NoError(t, err) || !assert.True(t, strings.HasPrefix(relative, ".dpin-release-storage-test-") && filepath.Base(relative) == relative) || !assert.True(t, strings.EqualFold(resolved, storagePath)) {
					return
				}
				assert.NoError(t, os.RemoveAll(storagePath))
			})
			t.Setenv("RELEASE_STORAGE_ROOT", storagePath)
			db, err := gorm.Open(driver, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			require.NoError(t, err)
			connection, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, connection.Close()) })
			if database != common.DatabaseTypeSQLite {
				name := db.Migrator().CurrentDatabase()
				require.True(t, name == "dpin_release_test" || strings.HasPrefix(name, "dpin_release_test_"), "refusing a non-disposable database")
			}
			entities := model.BelloBuddyReleaseModels()
			for _, entity := range entities {
				require.False(t, db.Migrator().HasTable(entity), "release matrix requires a fresh isolated schema")
			}
			t.Cleanup(func() {
				for i := len(entities) - 1; i >= 0; i-- {
					assert.NoError(t, db.Migrator().DropTable(entities[i]))
				}
			})
			require.NoError(t, db.AutoMigrate(entities...))
			release, err := BelloBuddyReleaseDraft(db, 1, 0, BelloBuddyReleaseDraftInput{
				Version: "0.0.0-storage-test.1", BuildID: "synthetic-only", Title: "Synthetic release storage test", MinimumSystemVersion: "10.0.19045",
			})
			require.NoError(t, err)
			payload := []byte("synthetic release artifact; this is not an executable\n")
			filename := BelloBuddyReleaseFilename(release.Version, "installer")
			assert.Equal(t, "DPin-"+release.Version+"-win-x64.exe", filename)
			updated, err := StoreBelloBuddyReleaseArtifact(db, 1, release.ID, release.Revision, "installer", filename, bytes.NewReader(payload))
			require.NoError(t, err, "a completed artifact must commit its upload status")
			require.Equal(t, model.BelloBuddyReleaseDraft, updated.State)
			require.Equal(t, release.Revision+1, updated.Revision)
			var upload model.BelloBuddyReleaseUpload
			require.NoError(t, db.First(&upload).Error)
			assert.Equal(t, "complete", upload.State)
			assert.Empty(t, upload.ErrorCode)
			assert.Equal(t, int64(len(payload)), upload.BytesReceived)
			selected, artifact, err := BelloBuddyReleaseArtifactByName(db, filename)
			require.NoError(t, err)
			assert.Equal(t, release.ID, selected.ID)
			assert.Equal(t, int64(len(payload)), artifact.Size)
			sum256, sum512 := sha256.Sum256(payload), sha512.Sum512(payload)
			assert.Equal(t, hex.EncodeToString(sum256[:]), artifact.SHA256)
			assert.Equal(t, base64.StdEncoding.EncodeToString(sum512[:]), artifact.SHA512)
			storage, err := OpenBelloBuddyReleaseStorage()
			require.NoError(t, err)
			defer storage.Close()
			actual, err := storage.ReadFile(artifact.StorageKey)
			require.NoError(t, err)
			assert.Equal(t, payload, actual)
			assert.Error(t, CheckBelloBuddyReleaseAccess(selected, false), "storage tests must never publish a version")
			partial := []byte("interrupted synthetic upload")
			for _, failure := range []struct {
				name string
				body io.Reader
				size int64
			}{
				{"empty body", bytes.NewReader(nil), 0},
				{"interrupted body", io.MultiReader(bytes.NewReader(partial), iotest.ErrReader(errors.New("synthetic reader failure"))), int64(len(partial))},
			} {
				t.Run(failure.name, func(t *testing.T) {
					_, err := StoreBelloBuddyReleaseArtifact(db, 1, release.ID, updated.Revision, "installer", filename, failure.body)
					require.Error(t, err)
					var failed model.BelloBuddyReleaseUpload
					require.NoError(t, db.Order("id DESC").First(&failed).Error)
					assert.Equal(t, "failed", failed.State)
					assert.Equal(t, "UPLOAD_FAILED", failed.ErrorCode)
					assert.Equal(t, failure.size, failed.BytesReceived)
				})
			}
			unchanged, err := model.GetBelloBuddyRelease(db, release.ID)
			require.NoError(t, err)
			assert.Equal(t, updated.Revision, unchanged.Revision)
			actual, err = storage.ReadFile(artifact.StorageKey)
			require.NoError(t, err)
			assert.Equal(t, payload, actual, "failed uploads must not replace the previous artifact")
			legacy, err := BelloBuddyReleaseDraft(db, 1, 0, BelloBuddyReleaseDraftInput{
				Version: "0.0.0-storage-test.2", BuildID: "synthetic-legacy", Title: "Legacy release storage test", MinimumSystemVersion: "10.0.19045",
			})
			require.NoError(t, err)
			legacyInstaller := "BelloBuddy-Recovered-" + legacy.Version + "-win-x64.exe"
			for _, kind := range []string{"installer", "blockmap"} {
				name := legacyInstaller
				if kind == "blockmap" {
					name += ".blockmap"
				}
				legacy, err = StoreBelloBuddyReleaseArtifact(db, 1, legacy.ID, legacy.Revision, kind, name, bytes.NewReader(payload))
				require.NoError(t, err, "historical artifact names remain uploadable")
				selected, artifact, err := BelloBuddyReleaseArtifactByName(db, name)
				require.NoError(t, err, "old installer and blockmap URLs must resolve their exact stored bytes")
				assert.Equal(t, legacy.ID, selected.ID)
				assert.Equal(t, name, artifact.Filename)
				assert.Equal(t, hex.EncodeToString(sum256[:]), artifact.SHA256)
				_, _, err = BelloBuddyReleaseArtifactByName(db, BelloBuddyReleaseFilename(legacy.Version, kind))
				assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "a new-name URL must not alias historical files")
				if kind == "installer" {
					feed, err := BelloBuddyReleaseFeed(selected, artifact)
					require.NoError(t, err)
					assert.Contains(t, string(feed), "/desktop-updates/bellobuddy/artifacts/win32/x64/"+name)
					assert.NotContains(t, string(feed), BelloBuddyReleaseFilename(legacy.Version, kind))
				}
			}
			stage, err := storage.Open("staging")
			require.NoError(t, err)
			entries, err := stage.ReadDir(-1)
			require.NoError(t, err)
			require.NoError(t, stage.Close())
			assert.Empty(t, entries, "successful and failed upload staging files must be removed")
			for _, entity := range []any{&model.BelloBuddyReleaseChannel{}, &model.BelloBuddyReleaseOperation{}} {
				var count int64
				require.NoError(t, db.Model(entity).Count(&count).Error)
				assert.Zero(t, count, "storage tests must not create a publication channel or operation")
			}
		})
	}
}
