package main

import (
	"database/sql"
	"fmt"

	_ "github.com/duckdb/duckdb-go/v2"
)

func main() {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		panic(err)
	}
	var v string
	if err := db.QueryRow("SELECT version()").Scan(&v); err != nil {
		panic(err)
	}
	fmt.Println("duckdb", v)
}
