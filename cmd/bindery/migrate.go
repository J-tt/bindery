package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/vavallee/bindery/internal/config"
	"github.com/vavallee/bindery/internal/db"
	"github.com/vavallee/bindery/internal/metadata"
	"github.com/vavallee/bindery/internal/migrate"
)

// runMigrate handles the `bindery migrate <type> <path>` subcommand.
// It prints a JSON summary to stdout and exits with a non-zero code on
// error so shell scripts can detect failures.
func runMigrate(
	_ *config.Config,
	authors *db.AuthorRepo,
	indexers *db.IndexerRepo,
	clients *db.DownloadClientRepo,
	blocklist *db.BlocklistRepo,
	agg *metadata.Aggregator,
) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: bindery migrate <csv|readarr> <path>")
		os.Exit(2)
	}
	kind, path := os.Args[2], os.Args[3]
	ctx := context.Background()

	switch kind {
	case "csv":
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open csv:", err)
			os.Exit(1)
		}
		defer f.Close()
		res, err := migrate.ImportCSVAuthors(ctx, f, authors, agg, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "csv import:", err)
			_ = f.Close()
			os.Exit(1) //nolint:gocritic // exitAfterDefer: file already closed above
		}
		printJSON(res)

	case "readarr":
		res, err := migrate.ImportReadarr(ctx, path, authors, indexers, clients, blocklist, agg, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "readarr import:", err)
			os.Exit(1)
		}
		printJSON(res)

	default:
		fmt.Fprintln(os.Stderr, "unknown migrate type:", kind, "(expected csv|readarr)")
		os.Exit(2)
	}
}

func printJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
