package assets_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The structural guard of the asset ledger (docs/architecture/37-fixed-assets.md): fa_ledger and the state columns of fa_assets are written by one file, internal/assets/ledger.go. Any other
// call of a generated Ledger* query, or any other SQL that writes those tables, fails here.
var assetWriterSQL = regexp.MustCompile(`(?is)\b(insert\s+into|update|delete\s+from|truncate(\s+table)?)\s+(only\s+)?(fa_ledger|fa_assets)\b`)

func TestOnlyLedgerGoWritesTheAssetLedger(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var bad []string
	must(t, filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.Contains(rel, "/assetsdb/") || strings.HasSuffix(rel, "_test.go") || rel == "assets/ledger.go" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		switch {
		case strings.HasSuffix(rel, ".go"):
			if strings.Contains(src, ".Ledger") && regexp.MustCompile(`\.Ledger(InsertAsset|InsertEntry|SetCancelled|RestateAsset)\b`).MatchString(src) {
				bad = append(bad, rel+": calls a ledger writer")
			}
			if assetWriterSQL.MatchString(src) {
				bad = append(bad, rel+": writes fa_ledger or fa_assets in SQL")
			}
		case rel == "assets/queries.sql":
			for _, block := range strings.Split(src, "-- name: ")[1:] {
				name, _, _ := strings.Cut(block, " ")
				if assetWriterSQL.MatchString(block) && !strings.HasPrefix(name, "Ledger") && name != "UpdateAssetDetails" {
					bad = append(bad, "assets/queries.sql: "+name+" writes the ledger tables and is not named Ledger*")
				}
			}
		case strings.HasSuffix(rel, "queries.sql"):
			if assetWriterSQL.MatchString(src) {
				bad = append(bad, rel+": writes the ledger tables outside the assets module")
			}
		}
		return nil
	}))
	if len(bad) > 0 {
		t.Fatalf("the asset ledger has other writers than ledger.go:\n%s", strings.Join(bad, "\n"))
	}
}
