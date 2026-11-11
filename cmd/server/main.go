package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"aurumledger/internal/handler"
	"aurumledger/internal/repository"
	"aurumledger/internal/service"
	"aurumledger/internal/worker"
	"aurumledger/pkg/postgres"
	"aurumledger/pkg/rabbitmq"
	"aurumledger/pkg/redis"
)

func main() {
	log.Println("==================================================================")
	log.Println("   AURUMLEDGER - High-Concurrency Digital Gold Accounting Engine  ")
	log.Println("==================================================================")

	// Load .env configuration
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using defaults / environment variables")
	} else {
		log.Println(".env configuration loaded successfully")
	}

	// Environment configuration defaults
	pgDSN := os.Getenv("DATABASE_URL")
	if pgDSN == "" {
		pgDSN = "host=localhost user=postgres password=postgres dbname=wallet_db port=5432 sslmode=disable"
	}
	redisAddr := os.Getenv("REDIS_URL")
	if redisAddr == "" {
		redisAddr = os.Getenv("REDIS_ADDR")
	}
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}
	rabbitURL := os.Getenv("RABBITMQ_URL")
	if rabbitURL == "" {
		rabbitURL = "amqp://guest:guest@localhost:5672/"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Root context for background processes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Database Connection (PostgreSQL with embedded fallback for zero-friction local run)
	db, err := postgres.NewConnection(pgDSN)
	if err != nil {
		log.Printf("Warning: PostgreSQL connection to %s failed (%v)", pgDSN, err)
		log.Println("==> Automatically falling back to embedded database (sqlite://aurumledger.db) for zero-setup local execution")
		db, err = postgres.NewConnection("sqlite://aurumledger.db")
		if err != nil {
			log.Fatalf("Fatal: Database initialization failed: %v", err)
		}
	}

	// 2. Initialize Redis (optional cache layer)
	rdb, err := redis.NewClient(redisAddr, "", 0)
	if err != nil {
		log.Printf("Warning: Redis connection failed (%v). Running with cache bypass.", err)
		rdb = nil
	}

	// 3. Initialize RabbitMQ
	mq, err := rabbitmq.NewConnection(rabbitURL)
	if err != nil {
		log.Printf("Warning: RabbitMQ connection failed (%v). Outbox events will persist in DB.", err)
		mq = nil
	}

	// 4. Initialize Repositories
	walletRepo := repository.NewWalletRepository(db)
	transRepo := repository.NewTransactionRepository(db)
	idempotencyRepo := repository.NewIdempotencyRepository(db)
	ledgerRepo := repository.NewLedgerRepository(db)
	outboxRepo := repository.NewOutboxRepository(db)
	processedRepo := repository.NewProcessedEventRepository(db)
	cacheRepo := repository.NewCacheRepository(rdb)

	// 5. Initialize Services
	walletSvc := service.NewWalletService(walletRepo, transRepo, idempotencyRepo, ledgerRepo, outboxRepo, cacheRepo)
	reconSvc := service.NewReconciliationService(walletRepo, ledgerRepo)

	// Auto-seed 5 demo accounts if fewer than 5 exist
	if existing, err := walletRepo.GetAll(ctx); err == nil && len(existing) < 5 {
		log.Println("Initializing Gullak demo accounts for interactive transaction walkthrough...")
		demoUsers := []struct {
			UserID  uuid.UUID
			Name    string
			Balance int64
		}{
			{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440001"), Name: "Alice Sharma (Daily Saver)", Balance: 2500},
			{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440002"), Name: "Bob Patel (Investor)", Balance: 1500},
			{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440003"), Name: "Charlie Verma (Gold Lease)", Balance: 1000},
			{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440004"), Name: "Diana Singh (Merchant)", Balance: 750},
			{UserID: uuid.MustParse("550e8400-e29b-41d4-a716-446655440005"), Name: "Gullak Treasury Vault", Balance: 10000},
		}
		for _, du := range demoUsers {
			found := false
			for _, e := range existing {
				if e.UserID == du.UserID {
					found = true
					break
				}
			}
			if !found {
				_, _ = walletSvc.CreateWalletWithName(ctx, du.UserID, du.Name, du.Balance)
			}
		}
	}

	// 6. Start Priority 3 Outbox Publisher Background Process
	outboxPublisher := service.NewOutboxPublisher(outboxRepo, mq, 250*time.Millisecond, 50)
	go outboxPublisher.Start(ctx)

	// 7. Start Priority 4 Reliable Consumer Background Worker
	var transferWorker *worker.Worker
	if mq != nil {
		transferWorker = worker.NewWorker(mq, processedRepo)
		if err := transferWorker.Start(ctx); err != nil {
			log.Printf("Warning: Failed to start transfer worker: %v", err)
		}
	}

	// 8. HTTP API Routing
	h := handler.NewHandler(walletSvc, reconSvc)
	mux := handler.NewRouter(h)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Start HTTP Server
	go func() {
		log.Printf("AurumLedger API server listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failed: %v", err)
		}
	}()

	// 9. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Initiating graceful shutdown of AurumLedger...")
	cancel() // Stop background workers and publisher

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server forced to shutdown: %v", err)
	}

	if transferWorker != nil {
		transferWorker.Stop()
	}

	if mq != nil {
		mq.Close()
	}

	log.Println("AurumLedger exited cleanly")
}
