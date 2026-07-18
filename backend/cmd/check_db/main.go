package main

import (
	"database/sql"
	"fmt"
	"log"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	db, err := sql.Open("sqlite3", "../../data/tv-player.db")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query("SELECT IFNULL(stream_type, 'NULL_VALUE'), COUNT(*) FROM channels GROUP BY stream_type")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var st string
		var count int
		if err := rows.Scan(&st, &count); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("stream_type: '%s', count: %d\n", st, count)
	}
}
