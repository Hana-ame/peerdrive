package main

import (
	"database/sql"
	"log"
	"os"

	"peerdrive-registration/internal/controller"
	"peerdrive-registration/internal/repository"
	"peerdrive-registration/internal/router"
	"peerdrive-registration/internal/service"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./registration.db"
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	userRepo := repository.NewUserRepository(db)
	if err := userRepo.InitSchema(); err != nil {
		log.Fatalf("failed to init schema: %v", err)
	}

	authSvc := service.NewAuthService(userRepo)
	authCtrl := controller.NewAuthController(authSvc)

	r := router.SetupRouter(authCtrl, authSvc)

	log.Printf("Registration server starting on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("failed to start: %v", err)
	}
}
