package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	"tookmyshow/handler"
	"tookmyshow/repository"
	"tookmyshow/workers"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	_ = godotenv.Load()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		slog.Error("dbURL not set")
		os.Exit(1)
	}
	conn, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		slog.Error("Unable to connect to database", "error", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_URL"),
	})
	instance := repository.EventRepository{
		Conn:  conn,
		Redis: redisClient,
	}
	var rabbitChannel *amqp.Channel
	var rabbitMu sync.RWMutex

	var connectRabbit func()
	connectRabbit = func() {
		for {
			conn, err := amqp.Dial(os.Getenv("RABBITMQ_URL"))
			if err == nil {
				ch, err := conn.Channel()
				if err == nil {
					rabbitMu.Lock()
					rabbitChannel = ch
					rabbitMu.Unlock()
					
					go func() {
						closeErr := <-conn.NotifyClose(make(chan *amqp.Error))
						slog.Warn("RabbitMQ connection closed. Reconnecting...", "error", closeErr)
						connectRabbit()
					}()
					slog.Info("Connected to RabbitMQ!")
					return
				}
				conn.Close()
			}
			slog.Warn("Failed to connect to RabbitMQ, retrying in 5s", "error", err)
			time.Sleep(5 * time.Second)
		}
	}
	go connectRabbit()

	h := &handler.EventHandler{
		Repo:   &instance,
		Redis:  redisClient,
		Rabbit: func() *amqp.Channel {
			rabbitMu.RLock()
			defer rabbitMu.RUnlock()
			return rabbitChannel
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/events", handler.MetricsMiddleware(h.GetAllEventsHandler))
	mux.HandleFunc("/events/{id}", handler.MetricsMiddleware(h.GetEventByIDHandler))
	mux.HandleFunc("/events/{id}/payment-intent", handler.MetricsMiddleware(handler.RequireAuth(h.CreatePaymentIntentHandler)))
	mux.HandleFunc("POST /events/{id}/book", handler.MetricsMiddleware(handler.RequireAuth(h.BookTicketHandler)))
	mux.HandleFunc("DELETE /events/{id}/book", handler.MetricsMiddleware(handler.RequireAuth(h.CancelReservationHandler)))
	mux.HandleFunc("/login", handler.MetricsMiddleware(h.AuthHandler))
	mux.HandleFunc("/events/{id}/checkout", handler.MetricsMiddleware(handler.RequireAuth(h.CheckoutHandler)))
	mux.HandleFunc("/stripe/webhook", handler.MetricsMiddleware(h.StripeWebhookHandler))

	go workers.StartEmailConsumer(os.Getenv("RABBITMQ_URL"))
	httpServer := http.Server{
		Addr:    ":8000",
		Handler: mux,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		slog.Info("Server running on :8000")
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server Crashed", "error", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	stop()
	slog.Info("Kill signal received. Shutting down gracefully---")
	shutDownctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutDownctx); err != nil {
		slog.Error("Server Forced to Shutdown", "error", err)
		os.Exit(1)
	}
	conn.Close()
	redisClient.Close()
	slog.Info("Server and Database closed gracefully---")

}
