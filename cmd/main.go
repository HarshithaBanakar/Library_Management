package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"library/internal/handlers"
	"library/internal/models"
	"library/internal/repositories"
	"library/internal/services"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL environment variable is required")
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("failed to get generic DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(time.Hour)

	// Ensure enums and models are in sync for GORM metadata (no auto-migration).
	db.Config.DisableAutomaticPing = false

	userRepo := repositories.NewUserRepository(db)
	bookRepo := repositories.NewBookRepository(db)
	bookCopyRepo := repositories.NewBookCopyRepository(db)
	checkoutRepo := repositories.NewCheckoutRepository(db)
	reservationRepo := repositories.NewReservationRepository(db)

	libraryService := services.NewLibraryService(db, userRepo, bookRepo, bookCopyRepo, checkoutRepo, reservationRepo)

	router := gin.Default()

	handlers.RegisterRoutes(router, libraryService)

	serverAddr := os.Getenv("SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = ":8080"
	}

	srv := &http.Server{
		Addr:         serverAddr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	log.Printf("Starting server on %s", serverAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

