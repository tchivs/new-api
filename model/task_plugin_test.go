package model

import (
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/utils/tests"
)

func setupTaskPluginModelTest(t *testing.T) {
	t.Helper()
	originalDB := DB
	t.Cleanup(func() { DB = originalDB })
	var err error
	DB, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, DB.AutoMigrate(&TaskPlugin{}))
}

func TestTaskPluginVersionActivationAndSourceImmutability(t *testing.T) {
	setupTaskPluginModelTest(t)

	v1 := TaskPlugin{Key: "mock", APIVersion: 1, Version: "1.0.0", Source: "v1", SourceHash: "hash-v1", Enabled: true}
	require.NoError(t, SaveTaskPlugin(&v1))
	assert.True(t, v1.Active)

	v2 := TaskPlugin{Key: "mock", APIVersion: 1, Version: "2.0.0", Source: "v2", SourceHash: "hash-v2", Enabled: true}
	require.NoError(t, SaveTaskPlugin(&v2))
	assert.False(t, v2.Active)
	require.NoError(t, ActivateTaskPlugin("mock", "2.0.0"))

	active, err := ListActiveTaskPlugins()
	require.NoError(t, err)
	require.Len(t, active, 1)
	assert.Equal(t, "2.0.0", active[0].Version)

	conflict := TaskPlugin{Key: "mock", APIVersion: 1, Version: "2.0.0", Source: "changed", SourceHash: "different", Enabled: true}
	err = SaveTaskPlugin(&conflict)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different source")

	require.NoError(t, SetTaskPluginEnabled("mock", false))
	active, err = ListActiveTaskPlugins()
	require.NoError(t, err)
	assert.Empty(t, active)

	all, err := ListTaskPlugins()
	require.NoError(t, err)
	assert.Len(t, all, 2)
	deleteResult, err := DeleteTaskPluginVersion("mock", "2.0.0")
	require.NoError(t, err)
	assert.True(t, deleteResult.DeletedActive)
	require.NotNil(t, deleteResult.Promoted)
	assert.Equal(t, "1.0.0", deleteResult.Promoted.Version)
	versions, err := ListTaskPluginVersions("mock")
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.Equal(t, "1.0.0", versions[0].Version)
	assert.True(t, versions[0].Active)
}

func TestDeleteActiveTaskPluginPromotesNewestRemainingVersion(t *testing.T) {
	setupTaskPluginModelTest(t)

	plugins := []*TaskPlugin{
		{Key: "promote", APIVersion: 1, Version: "1.0.0", Source: "v1", SourceHash: "hash-v1", Enabled: true},
		{Key: "promote", APIVersion: 1, Version: "2.0.0", Source: "v2", SourceHash: "hash-v2", Enabled: false},
		{Key: "promote", APIVersion: 1, Version: "3.0.0", Source: "v3", SourceHash: "hash-v3", Enabled: true},
		{Key: "promote", APIVersion: 1, Version: "4.0.0", Source: "v4", SourceHash: "hash-v4", Enabled: true},
	}
	for _, plugin := range plugins {
		require.NoError(t, SaveTaskPlugin(plugin))
	}
	require.NoError(t, DB.Model(plugins[1]).Update("created_at", 200).Error)
	require.NoError(t, DB.Model(plugins[2]).Update("created_at", 100).Error)
	require.NoError(t, DB.Model(plugins[3]).Update("created_at", 100).Error)

	deleteResult, err := DeleteTaskPluginVersion("promote", "1.0.0")
	require.NoError(t, err)
	assert.True(t, deleteResult.DeletedActive)
	require.NotNil(t, deleteResult.Promoted)
	assert.Equal(t, "2.0.0", deleteResult.Promoted.Version)
	assert.False(t, deleteResult.Promoted.Enabled)

	deleteResult, err = DeleteTaskPluginVersion("promote", "2.0.0")
	require.NoError(t, err)
	assert.True(t, deleteResult.DeletedActive)
	require.NotNil(t, deleteResult.Promoted)
	assert.Equal(t, "4.0.0", deleteResult.Promoted.Version)

	active, err := GetTaskPluginVersion("promote", "")
	require.NoError(t, err)
	assert.Equal(t, "4.0.0", active.Version)
}

func TestTaskPluginSyncSnapshotRevisionTracksDesiredRuntimeState(t *testing.T) {
	setupTaskPluginModelTest(t)

	empty, err := GetTaskPluginSyncSnapshot()
	require.NoError(t, err)
	assert.Empty(t, empty.Plugins)
	require.NotEmpty(t, empty.Revision)

	v1 := TaskPlugin{
		Key: "revision-probe", APIVersion: 1, Version: "1.0.0",
		Source: "v1", SourceHash: "hash-v1", Enabled: true,
	}
	require.NoError(t, SaveTaskPlugin(&v1))
	v1Snapshot, err := GetTaskPluginSyncSnapshot()
	require.NoError(t, err)
	require.Len(t, v1Snapshot.Plugins, 1)
	assert.NotEqual(t, empty.Revision, v1Snapshot.Revision)

	v2 := TaskPlugin{
		Key: "revision-probe", APIVersion: 1, Version: "2.0.0",
		Source: "v2", SourceHash: "hash-v2", Enabled: true,
	}
	require.NoError(t, SaveTaskPlugin(&v2))
	inactiveAdded, err := GetTaskPluginSyncSnapshot()
	require.NoError(t, err)
	assert.Equal(t, v1Snapshot.Revision, inactiveAdded.Revision)

	require.NoError(t, DB.Model(&v1).Update("remark", "operator note").Error)
	remarkChanged, err := GetTaskPluginSyncSnapshot()
	require.NoError(t, err)
	assert.Equal(t, v1Snapshot.Revision, remarkChanged.Revision)

	require.NoError(t, ActivateTaskPlugin("revision-probe", "2.0.0"))
	v2Snapshot, err := GetTaskPluginSyncSnapshot()
	require.NoError(t, err)
	require.Len(t, v2Snapshot.Plugins, 1)
	assert.Equal(t, "2.0.0", v2Snapshot.Plugins[0].Version)
	assert.NotEqual(t, v1Snapshot.Revision, v2Snapshot.Revision)

	require.NoError(t, SetTaskPluginEnabled("revision-probe", false))
	disabled, err := GetTaskPluginSyncSnapshot()
	require.NoError(t, err)
	assert.Empty(t, disabled.Plugins)
	assert.NotEqual(t, v2Snapshot.Revision, disabled.Revision)
}

func TestTaskPluginOrderSQLQuotesMySQLKeyColumn(t *testing.T) {
	db, err := gorm.Open(tests.DummyDialector{}, &gorm.Config{DryRun: true})
	require.NoError(t, err)

	var sqls []string
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:capture_task_plugin_sql", func(tx *gorm.DB) {
		sqls = append(sqls, tx.Statement.SQL.String())
	}))

	originalDB := DB
	t.Cleanup(func() { DB = originalDB })
	DB = db

	_, err = ListTaskPlugins()
	require.NoError(t, err)
	_, err = GetTaskPluginSyncSnapshot()
	require.NoError(t, err)

	require.Len(t, sqls, 2)
	for _, sql := range sqls {
		assert.Contains(t, sql, "`key`")
		assert.NotRegexp(t, `(?i)ORDER BY[[:space:]]+key([[:space:],]|$)`, sql)
	}
}

type priorTaskPlugin struct {
	Id         int64
	Key        string `gorm:"size:128;not null;uniqueIndex:uk_task_plugin_key_version,priority:1"`
	APIVersion int    `gorm:"not null"`
	Version    string `gorm:"size:64;not null;uniqueIndex:uk_task_plugin_key_version,priority:2"`
	Source     string `gorm:"type:text;not null"`
	SourceHash string `gorm:"size:64;not null"`
	Icon       string `gorm:"size:524288"`
	Enabled    bool   `gorm:"not null"`
	Active     bool   `gorm:"not null;index"`
	CreatedAt  int64  `gorm:"not null"`
	Remark     string `gorm:"type:text"`
}

func (priorTaskPlugin) TableName() string { return "task_plugins" }
func TestTaskPluginLongTextMigration(t *testing.T) {

	for _, dialect := range []struct {
		name, env string
	}{
		{name: "sqlite"},
		{name: "mysql", env: "TEST_MYSQL_DSN"},
		{name: "postgres", env: "TEST_POSTGRES_DSN"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			var db *gorm.DB
			var err error
			if dialect.env == "" {
				db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			} else {
				if os.Getenv(dialect.env) == "" {
					t.Skip(dialect.env + " is not configured")
				}
				db, _, err = chooseDB(dialect.env, false)
			}
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			t.Cleanup(func() { require.NoError(t, db.Migrator().DropTable(&TaskPlugin{})) })
			originalDB := DB
			DB = db
			t.Cleanup(func() { DB = originalDB })

			for _, upgrade := range []bool{true, false} {
				require.NoError(t, db.Migrator().DropTable(&TaskPlugin{}))
				if upgrade {
					require.NoError(t, db.AutoMigrate(&priorTaskPlugin{}))
					prior := priorTaskPlugin{Key: "fixture", APIVersion: 1, Version: "1.0.0", Source: "prior source", SourceHash: "prior-hash", Icon: "prior icon", Enabled: true, Active: true, CreatedAt: 1}
					require.NoError(t, db.Create(&prior).Error)
				}
				for range 2 {
					require.NoError(t, db.AutoMigrate(&TaskPlugin{}))
				}
				require.True(t, db.Migrator().HasIndex(&TaskPlugin{}, "uk_task_plugin_key_version"))
				columns, err := db.Migrator().ColumnTypes(&TaskPlugin{})
				require.NoError(t, err)
				wantType := "text"
				if dialect.name == "mysql" {
					wantType = "longtext"
				}
				for _, column := range columns {
					if column.Name() == "source" || column.Name() == "icon" {
						assert.Equal(t, wantType, strings.ToLower(column.DatabaseTypeName()), column.Name())
					}
				}
				if upgrade {
					var prior TaskPlugin
					require.NoError(t, db.Where(&TaskPlugin{Key: "fixture"}).First(&prior).Error)
					assert.Equal(t, LongText("prior source"), prior.Source)
					assert.Equal(t, LongText("prior icon"), prior.Icon)
					assert.True(t, prior.Active)
					assert.True(t, prior.Enabled)
					assert.Equal(t, "prior-hash", prior.SourceHash)
				}
				source := strings.Repeat("x", 100_000)
				row := TaskPlugin{Key: "large", APIVersion: 1, Version: "1.0.0", Source: LongText(source), SourceHash: "large-hash", Icon: LongText(source), Enabled: true, Active: true}
				require.NoError(t, db.Create(&row).Error)
				var stored TaskPlugin
				require.NoError(t, db.First(&stored, row.Id).Error)
				assert.Equal(t, row.Source, stored.Source)
				assert.Equal(t, row.Icon, stored.Icon)
				snapshot, err := GetTaskPluginSyncSnapshot()
				require.NoError(t, err)
				wantPlugins := 1
				if upgrade {
					wantPlugins++
				}
				require.Len(t, snapshot.Plugins, wantPlugins)
				for _, plugin := range snapshot.Plugins {
					assert.Empty(t, plugin.Source)
					assert.Empty(t, plugin.Icon)
				}
				loaded, err := GetTaskPluginSource(row.Id)
				require.NoError(t, err)
				assert.Equal(t, row.Source, loaded)
				duplicate := TaskPlugin{Key: "large", APIVersion: 1, Version: "1.0.0", Source: "duplicate", SourceHash: "other"}
				require.Error(t, db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Create(&duplicate).Error)
			}
		})
	}
}
