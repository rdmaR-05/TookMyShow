package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"tookmyshow/handler"
	"tookmyshow/repository"
	"tookmyshow/workers"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

func main() {
	_ = godotenv.Load()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("dbURL not set")
	}
	conn, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to connect to database: %v\n", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_URL"),
	})
	instance := repository.EventRepository{
		Conn:  conn,
		Redis: redisClient,
	}
	rabbitConn, err := amqp.Dial(os.Getenv("RABBITMQ_URL"))
	if err != nil {
		log.Fatalf("Failed to connect to RabbitMQ: %v", err)
	}
	defer rabbitConn.Close()

	rabbitChannel, err := rabbitConn.Channel()
	if err != nil {
		log.Fatalf("Failed to open a channel: %v", err)
	}
	defer rabbitChannel.Close()

	h := &handler.EventHandler{
		Repo:   &instance,
		Redis:  redisClient,
		Rabbit: rabbitChannel, 
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/events", h.GetAllEventsHandler)
	mux.HandleFunc("/events/{id}", h.GetEventByIDHandler)
	mux.HandleFunc("/events/{id}/payment-intent", handler.RequireAuth(h.CreatePaymentIntentHandler))
	mux.HandleFunc("/events/{id}/book", handler.RequireAuth(h.BookTicketHandler))
	mux.HandleFunc("/login", h.AuthHandler)
	mux.HandleFunc("/events/{id}/checkout", handler.RequireAuth(h.CheckoutHandler))

	go workers.StartEmailConsumer(os.Getenv("RABBITMQ_URL"))
	httpServer := http.Server{
		Addr:    ":8000",
		Handler: mux,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Println("Server running on :8000")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server Crashed %v\n", err)
		}
	}()
	<-ctx.Done()
	stop()
	log.Println("Kill signal received. Shutting down gracefully---")
	shutDownctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutDownctx); err != nil {
		log.Fatalf("Server Forced to Shutdown--- %v", err)
	}
	conn.Close()
	log.Println("Server and Database closed gracefully---")

}
