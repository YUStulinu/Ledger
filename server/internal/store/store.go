// Package store keeps everything in one DuckDB file: the bank lines as imported, the categories
// assigned to them, the exchange rates, and the double-entry ledger rebuilt from all of that.
//
// The bank lines are the source of truth; the ledger (transactions and postings) is derived, and
// rebuilt after every change. Rebuilding is cheap (thousands of rows) and keeps the derivation in
// one place: matching transfers between your own accounts, pairing currency exchanges, opening
// balances, and the posting on the other side of every transaction.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/duckdb/duckdb-go/v2" // the "duckdb" database/sql driver
)

type Store struct {
	DB   *sql.DB
	Path string
}

// Open opens (or creates) the ledger at path; "" opens a throwaway in-memory ledger.
func Open(path string) (*Store, error) {
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, err
	}
	// One connection: DuckDB is in-process, and session settings below must apply to every query.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db, Path: path}
	if err := s.init(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS bank_lines (
	key          VARCHAR PRIMARY KEY, -- account|currency|date|amount|occurrence
	seq          BIGINT,              -- import order, for a stable order within a day
	bank         VARCHAR,
	account      VARCHAR,             -- IBAN, or the bank's name when the statement does not say
	currency     VARCHAR,
	date         DATE,
	time         VARCHAR,
	amount       BIGINT,              -- minor units, signed
	balance      BIGINT,
	description  VARCHAR,
	reference    VARCHAR,
	type         VARCHAR,
	merchant     VARCHAR,
	merchant_key VARCHAR,
	file_sha     VARCHAR,
	line         INTEGER
);
CREATE TABLE IF NOT EXISTS imports (
	file_sha    VARCHAR PRIMARY KEY,
	file_name   VARCHAR,
	bank        VARCHAR,
	format      VARCHAR,
	account     VARCHAR,
	owner       VARCHAR,
	first_date  DATE,
	last_date   DATE,
	rows        INTEGER,
	new_rows    INTEGER,
	imported_at TIMESTAMP
);
CREATE TABLE IF NOT EXISTS openings (
	account  VARCHAR,
	currency VARCHAR,
	date     DATE,
	amount   BIGINT,
	PRIMARY KEY (account, currency)
);
CREATE TABLE IF NOT EXISTS labels (
	key        VARCHAR PRIMARY KEY,
	category   VARCHAR,
	source     VARCHAR, -- user, rule, model, llm
	confidence DOUBLE
);
CREATE TABLE IF NOT EXISTS corrections (
	merchant_key VARCHAR,
	description  VARCHAR,
	amount       BIGINT,
	category     VARCHAR,
	created_at   TIMESTAMP
);
CREATE TABLE IF NOT EXISTS rates (
	date     DATE,
	currency VARCHAR,
	ron      DOUBLE,
	PRIMARY KEY (date, currency)
);
CREATE TABLE IF NOT EXISTS ledger_accounts (
	name     VARCHAR PRIMARY KEY,
	type     VARCHAR, -- asset, expense, income, equity
	bank     VARCHAR,
	iban     VARCHAR,
	currency VARCHAR
);
CREATE TABLE IF NOT EXISTS ledger_transactions (
	id          BIGINT PRIMARY KEY,
	date        DATE,
	kind        VARCHAR, -- expense, income, transfer, exchange, opening
	category    VARCHAR,
	merchant    VARCHAR,
	description VARCHAR
);
CREATE TABLE IF NOT EXISTS ledger_postings (
	tx_id    BIGINT,
	account  VARCHAR,
	currency VARCHAR,
	amount   BIGINT,
	line_key VARCHAR -- the bank line it comes from (for the bank-account side)
);
`

func (s *Store) init() error {
	ctx := context.Background()
	if _, err := s.DB.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("creating the schema: %w", err)
	}
	if _, err := s.DB.ExecContext(ctx, views); err != nil {
		return fmt.Errorf("creating the views: %w", err)
	}
	// Questions are answered with SQL the language model writes. Nothing in Ledger needs DuckDB to
	// touch files or the network itself (statements are parsed in Go), so turn that off for the
	// whole database, and lock the configuration so no query can turn it back on.
	for _, stmt := range []string{"SET enable_external_access = false", "SET lock_configuration = true"} {
		if _, err := s.DB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}
