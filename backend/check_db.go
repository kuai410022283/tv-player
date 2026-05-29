//go:build ignore

package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "tvplayer.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var sqlStmt string
	err = db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='channel_groups'").Scan(&sqlStmt)
	if err != nil {
		log.Fatal(err)
	}
	
	f, _ := os.Create("db_schema.txt")
	defer f.Close()
	fmt.Fprintf(f, "TABLE SCHEMA: %s\n", sqlStmt)
	
	rows, err := db.Query("SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name='channel_groups'")
	if err != nil {
		log.Fatal(err)
	}
	defer rows.Close()
	fmt.Fprintf(f, "INDEXES:\n")
	for rows.Next() {
		var name, isql sql.NullString
		rows.Scan(&name, &isql)
		fmt.Fprintf(f, "- %s: %s\n", name.String, isql.String)
	}
}
