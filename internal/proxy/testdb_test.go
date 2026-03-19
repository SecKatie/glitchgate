package proxy_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

// templateDB holds a pre-migrated SQLite database file that is created once
// and copied for each test, avoiding the cost of running 26 migrations per test.
var (
	templateDBPath string

	// templateKey holds the proxy key credentials baked into the template DB.
	templateKey struct {
		Plaintext string
		Hash      string
		Prefix    string
		ID        string
	}
)

// TestMain runs migrations once and stores the template DB for all tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "proxy-test-template-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create template dir: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	dbPath := filepath.Join(dir, "template.db")
	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create template store: %v\n", err)
		os.Exit(1)
	}

	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		fmt.Fprintf(os.Stderr, "migrate template store: %v\n", err)
		os.Exit(1)
	}

	plaintext, hash, prefix, err := auth.GenerateKey()
	if err != nil {
		_ = st.Close()
		fmt.Fprintf(os.Stderr, "generate key: %v\n", err)
		os.Exit(1)
	}

	keyID := "template-key-id"
	if err := st.CreateProxyKey(context.Background(), keyID, hash, prefix, "template-key"); err != nil {
		_ = st.Close()
		fmt.Fprintf(os.Stderr, "create proxy key: %v\n", err)
		os.Exit(1)
	}

	// Close to flush WAL.
	if err := st.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "close template store: %v\n", err)
		os.Exit(1)
	}

	templateDBPath = dbPath
	templateKey.Plaintext = plaintext
	templateKey.Hash = hash
	templateKey.Prefix = prefix
	templateKey.ID = keyID

	os.Exit(m.Run())
}

// cloneTestDB copies the pre-migrated template database into a temp directory
// and returns an open store. The store is closed automatically when the test ends.
func cloneTestDB(t *testing.T) *store.SQLiteStore {
	t.Helper()

	src, err := os.ReadFile(templateDBPath) //nolint:gosec // controlled test fixture path set in TestMain
	if err != nil {
		t.Fatalf("read template DB: %v", err)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := os.WriteFile(dbPath, src, 0o600); err != nil { //nolint:gosec // dbPath is t.TempDir() + hardcoded filename
		t.Fatalf("write test DB: %v", err)
	}

	st, err := store.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open test DB: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	return st
}
