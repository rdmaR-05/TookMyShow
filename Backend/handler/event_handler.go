package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
	"tookmyshow/repository"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/paymentintent"
	"github.com/stripe/stripe-go/v78/webhook"
)

type EventStore interface {
	GetAllEvents(ctx context.Context) ([]repository.EventDisplay, error)
	GetEventByID(ctx context.Context, instanceID uuid.UUID) (repository.EventDisplay, error)
	IsSeatAvailable(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (bool, error)
	GetSeatPrice(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (float64, error)
	FinalizeOrder(ctx context.Context, userEmail string, instanceID uuid.UUID, seatID uuid.UUID, price float64) error
	LockSeat(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (bool, error)
}

type EventHandler struct {
	Repo   EventStore
	Redis  *redis.Client
	Rabbit func() *amqp.Channel
}

func (h *EventHandler) GetAllEventsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	events, err := h.Repo.GetAllEvents(r.Context())
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(events)
}

func (h *EventHandler) GetEventByIDHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idString := r.PathValue("id")
	instanceID, err := uuid.Parse(idString)
	if err != nil {
		http.Error(w, "Invalid Event ID format", http.StatusBadRequest)
		return
	}

	eventDetails, err := h.Repo.GetEventByID(r.Context(), instanceID)
	if err != nil {
		http.Error(w, "Event not found or Internal Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(eventDetails)
}


type LockSeatRequest struct {
	SeatID uuid.UUID `json:"seatID"`
}

func (h *EventHandler) BookTicketHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idString := r.PathValue("id")
	instanceID, err := uuid.Parse(idString)
	if err != nil {
		http.Error(w, "Invalid Event ID format", http.StatusBadRequest)
		return
	}

	var req LockSeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body format", http.StatusBadRequest)
		return
	}

	if req.SeatID == uuid.Nil {
		http.Error(w, "Invalid seat_id: cannot be empty", http.StatusBadRequest)
		return
	}

	email, ok := r.Context().Value(userEmailKey).(string)
	if !ok {
		http.Error(w, "Unauthorized: Email not found in context", http.StatusUnauthorized)
		return
	}

	isAvailable, err := h.Repo.IsSeatAvailable(r.Context(), instanceID, req.SeatID)
	if err != nil {
		slog.Error("Database error checking seat availability", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if !isAvailable {
		http.Error(w, "Seat already permanently booked", http.StatusConflict)
		return
	}

	lockKey := fmt.Sprintf("seat_lock:%s:%s", instanceID.String(), req.SeatID.String())

	acquired, err := h.Redis.SetNX(r.Context(), lockKey, email, 10*time.Minute).Result()
	if err != nil {
		slog.Error("CRITICAL ERROR IN REDIS LOCK", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Idempotency Check
	if !acquired {
		existingOwner, err := h.Redis.Get(r.Context(), lockKey).Result()
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if existingOwner != email {
			http.Error(w, "Seat is currently reserved by another user", http.StatusConflict)
			return
		}
		
		// If we reach here, it means the user already holds the lock.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Seat already reserved by you! Proceed to checkout."})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Seat reserved for 10 minutes! Proceed to checkout."})
}

type CheckoutRequest struct {
	SeatID uuid.UUID `json:"seatID"`
}

func (h *EventHandler) CheckoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idString := r.PathValue("id")
	instanceID, err := uuid.Parse(idString)
	if err != nil {
		http.Error(w, "Invalid Event ID", http.StatusBadRequest)
		return
	}

	var req CheckoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	fmt.Printf("Parsed SeatID: '%v'\n", req.SeatID)

	price, err := h.Repo.GetSeatPrice(r.Context(), instanceID, req.SeatID)
	if err != nil {
		http.Error(w, "Seat price not found", http.StatusInternalServerError)
		return
	}


	email, ok := r.Context().Value(userEmailKey).(string)
	if !ok {
		http.Error(w, "Unauthorized: Email not found in context", http.StatusUnauthorized)
		return
	}
	lockKey := fmt.Sprintf("seat_lock:%s:%s", instanceID.String(), req.SeatID.String())
	lockOwner, err := h.Redis.Get(r.Context(), lockKey).Result()

	if err == redis.Nil {
		http.Error(w, "Reservation expired. Please book the seat again.", http.StatusForbidden)
		return
	} else if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if lockOwner != email {
		http.Error(w, "This seat is reserved by another user.", http.StatusForbidden)
		return
	}

	err = h.Repo.FinalizeOrder(r.Context(), email, instanceID, req.SeatID, price)
	if err != nil {
		
		if err.Error() == "checkout failed: seat is already sold" {
			http.Error(w, "Seat was sold to someone else", http.StatusConflict)
			return
		}
		http.Error(w, "Payment Processing Failed", http.StatusInternalServerError)
		return
	}

	h.Redis.Del(r.Context(), lockKey)

	conn, err := amqp.Dial(os.Getenv("RABBITMQ_URL"))
	if err != nil {
		fmt.Printf("WARNING: Failed to connect to RabbitMQ: %v\n", err)
	} else {
		defer conn.Close()
		ch, _ := conn.Channel()
		defer ch.Close()

		q, _ := ch.QueueDeclare("ticket_emails", true, false, false, false, nil)

	
		payload := fmt.Sprintf(`{"email":"%s", "seat_id":"%s"}`, email, req.SeatID.String())


		ch.PublishWithContext(r.Context(), "", q.Name, false, false, amqp.Publishing{
			DeliveryMode: amqp.Persistent, // agar RabbitMQ fail hua, phir bhi restart kke baad queue main rahega
			ContentType:  "application/json",
			Body:         []byte(payload),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Order Finalized! Ticket Generated."})
}

func (h *EventHandler) AuthHandler(w http.ResponseWriter, r *http.Request) {
	type loginRequest struct {
		Email string `json:"email"`
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var emailId loginRequest
	if err := json.NewDecoder(r.Body).Decode(&emailId); err != nil {
		http.Error(w, "Invalid Request Body", http.StatusBadRequest)
		return
	}

	jwtToken, err := GenerateToken(emailId.Email)
	if err != nil {
		http.Error(w, "Error in Generating Token", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": jwtToken})
}

type BookRequest struct {
	SeatID uuid.UUID `json:"seatID"`
}

type PaymentIntentRequest struct {
	SeatID uuid.UUID `json:"seatID"`
}

func (h *EventHandler) CreatePaymentIntentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idString := r.PathValue("id")
	instanceID, err := uuid.Parse(idString)
	if err != nil {
		http.Error(w, "Invalid Event ID", http.StatusBadRequest)
		return
	}

	var req PaymentIntentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	email, ok := r.Context().Value(userEmailKey).(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	lockKey := fmt.Sprintf("seat_lock:%s:%s", instanceID.String(), req.SeatID.String())
	lockOwner, err := h.Redis.Get(r.Context(), lockKey).Result()

	if err == redis.Nil {
		http.Error(w, "Reservation expired. Please book the seat again.", http.StatusForbidden)
		return
	} else if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if lockOwner != email {
		http.Error(w, "You do not own the reservation for this seat.", http.StatusForbidden)
		return
	}

	price, err := h.Repo.GetSeatPrice(r.Context(), instanceID, req.SeatID)
	if err != nil {
		http.Error(w, "Failed to get seat price", http.StatusInternalServerError)
		return
	}

	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")

	amountInCents := int64(price * 100)

	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(amountInCents),
		Currency: stripe.String(string(stripe.CurrencyUSD)),
		
		Metadata: map[string]string{
			"user_email":        email,
			"event_instance_id": instanceID.String(),
			"seat_id":           req.SeatID.String(),
			"price":             fmt.Sprintf("%.2f", price),
		},
	}

	pi, err := paymentintent.New(params)
	if err != nil {
		slog.Error("Stripe error", "error", err)
		http.Error(w, "Failed to initialize payment", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"clientSecret": pi.ClientSecret,
	})
}

func (h *EventHandler) StripeWebhookHandler(w http.ResponseWriter, r *http.Request) {
	const MaxBodyBytes = int64(65536)
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusServiceUnavailable)
		return
	}

	sigHeader := r.Header.Get("Stripe-Signature")
	endpointSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")

	event, err := webhook.ConstructEvent(payload, sigHeader, endpointSecret)
	if err != nil {
		slog.Error("Webhook signature verification failed", "error", err)
		http.Error(w, "Invalid signature", http.StatusBadRequest)
		return
	}

	if event.Type == "payment_intent.succeeded" {
		var pi stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &pi)
		if err != nil {
			http.Error(w, "Error parsing webhook JSON", http.StatusBadRequest)
			return
		}

	
		email := pi.Metadata["user_email"]
		instanceIDStr := pi.Metadata["event_instance_id"]
		seatIDStr := pi.Metadata["seat_id"]

		instanceID, _ := uuid.Parse(instanceIDStr)
		seatID, _ := uuid.Parse(seatIDStr)

		price, err := h.Repo.GetSeatPrice(context.Background(), instanceID, seatID)
		if err != nil {
			slog.Error("Failed to get seat price during webhook", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		err = h.Repo.FinalizeOrder(context.Background(), email, instanceID, seatID, price)
		if err != nil {
			slog.Error("CRITICAL: Payment succeeded but DB failed", "email", email, "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}


		lockKey := fmt.Sprintf("seat_lock:%s:%s", instanceIDStr, seatIDStr)
		h.Redis.Del(context.Background(), lockKey)


		emailPayload, _ := json.Marshal(map[string]string{
			"email":   email,
			"seat_id": seatIDStr,
		})

		rabbitChan := h.Rabbit()
		if rabbitChan != nil {
			err = rabbitChan.Publish(
				"",              // exchange
				"ticket_emails", // routing key (queue name)
				false,           // mandatory
				false,           // immediate
				amqp.Publishing{
					DeliveryMode: amqp.Persistent,
					ContentType:  "application/json",
					Body:         emailPayload,
				},
			)
			if err != nil {
				slog.Error("ERROR: Database updated, but failed to queue email", "email", email, "error", err)
			} else {
				slog.Info("Successfully processed ticket and queued email", "email", email)
			}
		} else {
			slog.Error("ERROR: RabbitMQ channel is nil. Failed to queue email", "email", email)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *EventHandler) CancelReservationHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	idString := r.PathValue("id")
	instanceID, err := uuid.Parse(idString)
	if err != nil {
		http.Error(w, "Invalid Event ID format", http.StatusBadRequest)
		return
	}

	var req LockSeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body format", http.StatusBadRequest)
		return
	}

	email, ok := r.Context().Value(userEmailKey).(string)
	if !ok {
		http.Error(w, "Unauthorized: Email not found in context", http.StatusUnauthorized)
		return
	}

	lockKey := fmt.Sprintf("seat_lock:%s:%s", instanceID.String(), req.SeatID.String())
	
	existingOwner, err := h.Redis.Get(r.Context(), lockKey).Result()
	if err == redis.Nil {
		http.Error(w, "Reservation already expired or not found", http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if existingOwner != email {
		http.Error(w, "Seat is currently reserved by another user", http.StatusConflict)
		return
	}

	err = h.Redis.Del(r.Context(), lockKey).Err()
	if err != nil {
		http.Error(w, "Failed to release reservation", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Reservation cancelled successfully."})
}
