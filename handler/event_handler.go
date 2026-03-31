package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
	"tookmyshow/repository"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/paymentintent"
	"github.com/stripe/stripe-go/v78/webhook"
)

type EventHandler struct {
	Repo   *repository.EventRepository
	Redis  *redis.Client
	Rabbit *amqp.Channel
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
		log.Println("Database error checking seat availability:", err)
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
		log.Println("CRITICAL ERROR IN REDIS LOCK:", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Idempotency Check
	// if !acquired {
	// 	existingOwner, err := h.Redis.Get(r.Context(), lockKey).Result()
	// 	if err != nil {
	// 		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	// 		return
	// 	}

	// 	if existingOwner != email {
	// 		http.Error(w, "Seat is currently reserved by another user", http.StatusConflict)
	// 		return
	// 	}
	// }
	if !acquired {
		http.Error(w, "Seat is currently reserved by another user", http.StatusConflict)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Seat reserved for 10 minutes! Proceed to checkout."})
}

type CheckoutRequest struct {
	SeatID uuid.UUID `json:"seatID"`
	Price  float64   `json:"price"`
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


	err = h.Repo.FinalizeOrder(r.Context(), email, instanceID, req.SeatID, req.Price)
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
	Price  float64   `json:"price"`
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

	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")

	amountInCents := int64(req.Price * 100)

	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(amountInCents),
		Currency: stripe.String(string(stripe.CurrencyUSD)),
		
		Metadata: map[string]string{
			"user_email":        email,
			"event_instance_id": instanceID.String(),
			"seat_id":           req.SeatID.String(),
			"price":             fmt.Sprintf("%.2f", req.Price),
		},
	}

	pi, err := paymentintent.New(params)
	if err != nil {
		log.Printf("Stripe error: %v", err)
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
		log.Printf("Webhook signature verification failed: %v", err)
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
		priceStr := pi.Metadata["price"]

		instanceID, _ := uuid.Parse(instanceIDStr)
		seatID, _ := uuid.Parse(seatIDStr)
		price, _ := strconv.ParseFloat(priceStr, 64)


		err = h.Repo.FinalizeOrder(context.Background(), email, instanceID, seatID, price)
		if err != nil {
			log.Printf("CRITICAL: Payment succeeded but DB failed for %s: %v", email, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}


		lockKey := fmt.Sprintf("seat_lock:%s:%s", instanceIDStr, seatIDStr)
		h.Redis.Del(context.Background(), lockKey)


		emailPayload, _ := json.Marshal(map[string]string{
			"email":   email,
			"seat_id": seatIDStr,
		})

		err = h.Rabbit.Publish(
			"",              // exchange
			"ticket_emails", // routing key (queue name)
			false,           // mandatory
			false,           // immediate
			amqp.Publishing{
				DeliveryMode: amqp.Persistent, // Writes to disk to survive crashes
				ContentType:  "application/json",
				Body:         emailPayload,
			},
		)
		if err != nil {
			log.Printf("ERROR: Database updated, but failed to queue email for %s: %v", email, err)
			
		} else {
			log.Printf("Successfully processed ticket and queued email for %s!", email)
		}
	}
	w.WriteHeader(http.StatusOK)
}
