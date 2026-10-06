package main

import (
	"flag"
	"fmt"
	"log"

	"topup-backend/config"
	"topup-backend/internal/database"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "Run backfill in dry-run mode without modifying the database")
	flag.Parse()

	cfg := config.LoadConfig()
	db := database.InitDB(cfg)

	fmt.Println("==================================================")
	fmt.Println(" Provider Products Backfill Migration (Fase 3)")
	fmt.Printf(" Mode: dry-run=%v\n", *dryRun)
	fmt.Println("==================================================")

	res, err := database.BackfillProviderProducts(db, *dryRun)
	if err != nil {
		log.Fatalf("[Backfill] Failed: %v\n", err)
	}

	fmt.Printf("Summary:\n")
	fmt.Printf("  Total Nominals Checked: %d\n", res.TotalNominals)
	fmt.Printf("  Digiflazz Mappings:     %d\n", res.DigiflazzCount)
	fmt.Printf("  Kiosgamer Mappings:     %d\n", res.KiosgamerCount)
	fmt.Printf("  Total Rows To Create:   %d\n", res.TotalCreated)
	fmt.Printf("  Total Existing Skipped: %d\n", res.TotalSkipped)
	fmt.Println("==================================================")
	if *dryRun {
		fmt.Println("Dry-run complete. Run without --dry-run to apply changes.")
	} else {
		fmt.Println("Backfill applied successfully.")
	}
}
