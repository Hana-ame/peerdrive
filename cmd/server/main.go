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

	relayRepo := repository.NewRelayRepository(db)
	if err := relayRepo.InitSchema(); err != nil {
		log.Fatalf("failed to init relay schema: %v", err)
	}

	authSvc := service.NewAuthService(userRepo)
	relaySvc := service.NewRelayService(relayRepo)

	commentRepo := repository.NewCommentRepository(db)
	if err := commentRepo.InitSchema(); err != nil {
		log.Fatalf("failed to init comment schema: %v", err)
	}
	commentSvc := service.NewCommentService(commentRepo)

	authCtrl := controller.NewAuthController(authSvc, relaySvc)
	authCtrl.SetCommentService(commentSvc)

	if err := userRepo.InitGroupSchema(); err != nil {
		log.Fatalf("failed to init group schema: %v", err)
	}

	r := router.SetupRouter(authCtrl, authSvc)

	log.Printf("Registration server starting on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("failed to start: %v", err)
	}
}
