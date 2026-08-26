package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Println("connect error:", err)
		os.Exit(1)
	}
	defer pool.Close()

	clerkID := "user_3IPJGRlgpPfvzqlTQvGIDBNnLpe"

	var exists bool
	if err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE clerk_id = $1)", clerkID).Scan(&exists); err != nil {
		fmt.Println("query error (exists check):", err)
	} else {
		fmt.Println("row exists for target clerkId:", exists)
	}

	var total int
	var mostRecent, leastRecent *time.Time
	if err := pool.QueryRow(ctx, "SELECT COUNT(*), MAX(created_at), MIN(created_at) FROM users").Scan(&total, &mostRecent, &leastRecent); err != nil {
		fmt.Println("query error (aggregate):", err)
	} else {
		fmt.Println("total users:", total, "most recent created_at:", mostRecent, "least recent:", leastRecent)
	}

	rows, err := pool.Query(ctx, "SELECT clerk_id, email, subscription_type, created_at FROM users ORDER BY created_at DESC LIMIT 5")
	if err != nil {
		fmt.Println("query error (recent list):", err)
		return
	}
	defer rows.Close()
	fmt.Println("--- 5 most recent users ---")
	for rows.Next() {
		var id, email, sub string
		var createdAt time.Time
		if err := rows.Scan(&id, &email, &sub, &createdAt); err != nil {
			fmt.Println("scan error:", err)
			continue
		}
		fmt.Printf("%s | %s | %s | %s\n", id, email, sub, createdAt.Format(time.RFC3339))
	}
}
