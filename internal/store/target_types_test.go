package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentAndChannelTypeConstraints(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	readSchema := func(table string) string {
		t.Helper()
		var schema string
		if err := st.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&schema); err != nil {
			t.Fatalf("read %s schema: %v", table, err)
		}
		return schema
	}

	deployments := readSchema("deployments")
	for _, want := range []string{"tencent_cos", "tencent_teo", "aliyun_oss", "aliyun_dcdn", "qiniu_cdn"} {
		if !strings.Contains(deployments, want) {
			t.Fatalf("deployments schema is missing %q: %s", want, deployments)
		}
	}

	channels := readSchema("notification_channels")
	for _, want := range []string{"feishu", "dingtalk", "wecom"} {
		if !strings.Contains(channels, want) {
			t.Fatalf("notification_channels schema is missing %q: %s", want, channels)
		}
	}

	rows, err := st.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("migration left dangling foreign keys")
	}
}
