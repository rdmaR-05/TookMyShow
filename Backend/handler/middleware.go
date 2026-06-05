package handler

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "path", "status"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Histogram of response latency (seconds) of HTTP requests",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)
)

func init() {
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w}
}

func (rw *responseWriter) Status() int {
	return rw.status
}

func (rw *responseWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
	rw.wroteHeader = true
}

func MetricsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := wrapResponseWriter(w)
		next(wrapped, r)

		status := wrapped.status
		if status == 0 {
			status = http.StatusOK
		}

		duration := time.Since(start).Seconds()
		httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, strconv.Itoa(status)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, r.URL.Path).Observe(duration)
	}
}

type contextKey string

const userEmailKey contextKey = "userEmail"

func RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized: Missing Token", http.StatusUnauthorized)
			return
		}
		log.Printf("RAW HEADER RECEIVED: '%s'", authHeader)
		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		if tokenString == authHeader {
			http.Error(w, "Unauthorized: Malformed Token Format", http.StatusUnauthorized)
			return
		}

		secretKey := os.Getenv("JWT_SECRET")

		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return []byte(secretKey), nil
		})

		if err != nil || !token.Valid {
			http.Error(w, "Unauthorized: Invalid Token", http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, "Unauthorized: Invalid Claims", http.StatusUnauthorized)
			return
		}

		email, ok := claims["email"].(string)
		if !ok {
			http.Error(w, "Unauthorized: Email Missing in Token", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userEmailKey, email)

		next(w, r.WithContext(ctx))
	}
}
