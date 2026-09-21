package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

func main() {
	dir := flag.String("dir", "migrations", "migrations directory")
	direction := flag.String("direction", "up", "up or down")
	databaseURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "postgres url")
	flag.Parse()
	if *databaseURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, *databaseURL)
	if err != nil {
		fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)`); err != nil {
		fatal(err)
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		fatal(err)
	}
	var files []string
	suffix := "." + *direction + ".sql"
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	if *direction == "down" {
		for i, j := 0, len(files)-1; i < j; i, j = i+1, j-1 {
			files[i], files[j] = files[j], files[i]
		}
	}
	for _, name := range files {
		version := strings.Split(name, "_")[0]
		var applied int
		if err := conn.QueryRow(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=$1`, version).Scan(&applied); err != nil {
			fatal(err)
		}
		if *direction == "up" && applied > 0 {
			continue
		}
		if *direction == "down" && applied == 0 {
			continue
		}
		body, err := os.ReadFile(filepath.Join(*dir, name))
		if err != nil {
			fatal(err)
		}
		if _, err := conn.Exec(ctx, string(body)); err != nil {
			fatal(fmt.Errorf("%s: %w", name, err))
		}
		if *direction == "up" {
			if _, err := conn.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
				fatal(err)
			}
		} else {
			if _, err := conn.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, version); err != nil {
				fatal(err)
			}
		}
		fmt.Println(name)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
