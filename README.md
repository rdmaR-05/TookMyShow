# TookMyShow - High-Concurrency Ticketing Engine

A distributed reservation engine built in Go, designed to handle extreme concurrency and prevent race conditions during ticket booking surges (Thundering Herd scenarios).

## Architecture & Core Problems Solved

When thousands of users attempt to book the same seat at the exact same millisecond, traditional relational database locking (`SELECT FOR UPDATE`) causes massive connection pooling bottlenecks and CPU exhaustion. 

This project solves this by offloading the concurrency control to an in-memory distributed lock mechanism before the request ever reaches the database.

* **Distributed Atomic Locking:** Uses Redis `SETNX` with TTL expirations to guarantee that only the first request acquires the seat lock. The remaining conflicting requests are instantly rejected with a `409 Conflict` in sub-millisecond time, protecting the database from deadlocks.
* **Idempotency:** Webhook and booking endpoints are fully idempotent. If a client retries a successful booking due to network latency, the API recognizes the duplicate request and safely returns `200 OK` without duplicating database records or charges.
* **Asynchronous Workers:** Slow, blocking network calls (sending confirmation emails, processing Stripe webhooks) are completely decoupled from the main HTTP handlers. The API drops an AMQP message into RabbitMQ and returns immediately, while a background Go worker processes the queue.
* **Observability:** Fully instrumented with Prometheus to scrape runtime metrics and Grafana for visualization.

## Tech Stack

* **Language:** Go (Golang)
* **Database:** PostgreSQL (with custom ENUMs and constraints for data integrity)
* **Caching & Locking:** Redis
* **Message Broker:** RabbitMQ
* **Infrastructure:** Docker & Docker Compose
* **Observability:** Prometheus & Grafana
* **Load Testing:** k6

## Load Testing Metrics (Thundering Herd)

The system was stress-tested on a single 1-vCPU / 1GB RAM Linux node using a dynamically forged JWT `k6` script to simulate 500 unique concurrent users fighting for a single seat lock.

* **Throughput:** 10,000 requests processed in 7.0 seconds (1,430 RPS).
* **Double-Bookings:** 0.
* **Crashes (500s):** 0.
* **Behavior:** 1 user acquired the lock (`200 OK`), 9,979 users were successfully rejected via the Redis Mutex (`409 Conflict`).

## Running Locally

### Prerequisites
* Docker & Docker Compose
* Go 1.21+

### Setup
1. Clone the repository.
2. Create a `.env` file in the `Backend` directory:
   ```env
   STRIPE_SECRET_KEY=sk_test_your_key
   STRIPE_HOOK_SECRET_KEY=whsec_your_key
   JWT_SECRET=your_jwt_secret
   ```
3. Boot the infrastructure (Postgres, Redis, RabbitMQ, Prometheus, Grafana, Mailpit) and the Go API:
   ```bash
   cd Backend
   docker compose up -d --build
   ```

The API will be available on `localhost:8000`. Grafana will be available on `localhost:3000` (default login: admin/admin).

## Database Seeding
The database schema relies on native PostgreSQL enums and UUID generation (`pgcrypto`). To initialize the database, execute the SQL located in `db/migrations/000001_init_schema.up.sql` against the running Postgres container.
