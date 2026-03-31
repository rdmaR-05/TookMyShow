package repository

import (
	"time"

	"github.com/google/uuid"
)

type Organizer struct {
	ID        uuid.UUID `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type Venue struct {
	ID        uuid.UUID `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Timezone  string    `json:"timezone" db:"timezone"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type Event struct {
	ID          uuid.UUID `json:"id" db:"id"`
	OrganizerID uuid.UUID `json:"organizer_id" db:"organizer_id"`
	Name        string    `json:"name" db:"name"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

type EventInstance struct {
	ID        uuid.UUID `json:"id" db:"id"`
	EventID   uuid.UUID `json:"event_id" db:"event_id"`
	VenueID   uuid.UUID `json:"venue_id" db:"venue_id"`
	StartTime time.Time `json:"start_time" db:"start_time"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

type Seat struct {
	ID         uuid.UUID `json:"id" db:"id"`
	VenueID    uuid.UUID `json:"venue_id" db:"venue_id"`
	Section    string    `json:"section" db:"section"`
	RowName    string    `json:"row_name" db:"row_name"`
	SeatNumber int       `json:"seat_number" db:"seat_number"`
}

type EventZone struct {
	ID                 uuid.UUID `json:"id" db:"id"`
	EventInstanceID    uuid.UUID `json:"event_instance_id" db:"event_instance_id"`
	Name               string    `json:"name" db:"name"`
	IsGeneralAdmission bool      `json:"is_general_admission" db:"is_general_admission"`
	TotalCapacity      *int      `json:"total_capacity,omitempty" db:"total_capacity"`
	AvailableCapacity  *int      `json:"available_capacity,omitempty" db:"available_capacity"`
}

type PriceTier struct {
	ID          uuid.UUID `json:"id" db:"id"`
	EventZoneID uuid.UUID `json:"event_zone_id" db:"event_zone_id"`
	Name        string    `json:"name" db:"name"`
	Price       float64   `json:"price" db:"price"`
}

type EventSeat struct {
	EventInstanceID uuid.UUID `json:"event_instance_id" db:"event_instance_id"`
	SeatID          uuid.UUID `json:"seatID" db:"seat_id"`
	EventZoneID     uuid.UUID `json:"event_zone_id" db:"event_zone_id"`
	Status          string    `json:"status" db:"status"`
	Version         int       `json:"version" db:"version"`
}

type Booking struct {
	ID             uuid.UUID `json:"id" db:"id"`
	UserEmail      string    `json:"user_email" db:"user_email"`
	TotalAmount    float64   `json:"total_amount" db:"total_amount"`
	IdempotencyKey string    `json:"idempotency_key" db:"idempotency_key"`
	Status         string    `json:"status" db:"status"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

type Payment struct {
	ID                    uuid.UUID `json:"id" db:"id"`
	BookingID             uuid.UUID `json:"booking_id" db:"booking_id"`
	Provider              string    `json:"provider" db:"provider"`
	ProviderTransactionID string    `json:"provider_transaction_id" db:"provider_transaction_id"`
	Amount                float64   `json:"amount" db:"amount"`
	Status                string    `json:"status" db:"status"`
	CreatedAt             time.Time `json:"created_at" db:"created_at"`
}

type Ticket struct {
	ID              uuid.UUID  `json:"id" db:"id"`
	BookingID       uuid.UUID  `json:"booking_id" db:"booking_id"`
	EventInstanceID uuid.UUID  `json:"event_instance_id" db:"event_instance_id"`
	SeatID          *uuid.UUID `json:"seat_id,omitempty" db:"seat_id"`
	PricePaid       float64    `json:"price_paid" db:"price_paid"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
}
