package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type EventRepository struct {
	Conn  *pgxpool.Pool
	Redis *redis.Client
}

type EventDisplay struct {
	InstanceID    uuid.UUID `json:"instance_id"`
	EventName     string    `json:"event_name"`
	VenueName     string    `json:"venue_name"`
	OrganizerName string    `json:"organizer_name"`
	StartTime     time.Time `json:"start_time"`
}

func (repo *EventRepository) GetAllEvents(ctx context.Context) ([]EventDisplay, error) {
	query := `
		SELECT ei.id, e.name, v.name, o.name, ei.start_time
		FROM event_instance ei
		JOIN event e ON ei.event_id = e.id
		JOIN venue v ON ei.venue_id = v.id
		JOIN organizer o ON e.organizer_id = o.id
		ORDER BY ei.start_time ASC
	`
	rows, err := repo.Conn.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []EventDisplay
	for rows.Next() {
		var e EventDisplay
		if err := rows.Scan(&e.InstanceID, &e.EventName, &e.VenueName, &e.OrganizerName, &e.StartTime); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}
func (repo *EventRepository) GetEventByID(ctx context.Context, instanceID uuid.UUID) (EventDisplay, error) {
	query := `
		SELECT ei.id, e.name, v.name, o.name, ei.start_time
		FROM event_instance ei
		JOIN event e ON ei.event_id = e.id
		JOIN venue v ON ei.venue_id = v.id
		JOIN organizer o ON e.organizer_id = o.id
		WHERE ei.id = $1
	`
	var e EventDisplay
	err := repo.Conn.QueryRow(ctx, query, instanceID).Scan(
		&e.InstanceID, &e.EventName, &e.VenueName, &e.OrganizerName, &e.StartTime,
	)
	return e, err
}

func (repo *EventRepository) LockSeat(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (bool, error) {

	redisKey := fmt.Sprintf("cart:seat:%s", seatID.String())

	err := repo.Redis.SetArgs(ctx, redisKey, "locked", redis.SetArgs{
		Mode: "NX",
		TTL:  10 * time.Minute,
	}).Err()

	if err == redis.Nil {
		
		return false, nil
	}

	if err != nil {
		return false, err
	}
	return true, nil
}

func (repo *EventRepository) FinalizeOrder(ctx context.Context, userEmail string, instanceID uuid.UUID, seatID uuid.UUID, price float64) error {

	tx, err := repo.Conn.Begin(ctx)
	if err != nil {
		return err
	}

	defer tx.Rollback(ctx)

	var bookingID uuid.UUID
	err = tx.QueryRow(ctx, "INSERT INTO booking (user_email, total_amount) VALUES ($1, $2) RETURNING id", userEmail, price).Scan(&bookingID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, "INSERT INTO payment (booking_id, provider, amount, status) VALUES ($1, 'stripe', $2, 'succeeded')", bookingID, price)
	if err != nil {
		return err
	}

	tag, err := tx.Exec(ctx, "UPDATE event_seat SET status = 'sold' WHERE event_instance_id = $1 AND seat_id = $2 AND status != 'sold'", instanceID, seatID)
	if err != nil {
		return err
	}

	if tag.RowsAffected() == 0 {
		return fmt.Errorf("checkout failed: seat is already sold")
	}

	_, err = tx.Exec(ctx, "INSERT INTO ticket (booking_id, event_instance_id, seat_id, price_paid) VALUES ($1, $2, $3, $4)", bookingID, instanceID, seatID, price)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (repo *EventRepository) IsSeatAvailable(ctx context.Context, instanceID uuid.UUID, seatID uuid.UUID) (bool, error) {
	var status string
	err := repo.Conn.QueryRow(ctx, "SELECT status FROM event_seat WHERE event_instance_id = $1 AND seat_id = $2", instanceID, seatID).Scan(&status)
	if err != nil {
		return false, err
	}

	return status == "available", nil
}
