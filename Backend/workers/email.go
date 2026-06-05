package workers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/smtp"

	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type EmailPayload struct {
	Email  string `json:"email"`
	SeatID string `json:"seat_id"`
}

func StartEmailConsumer(rabbitURL string) {
	for {
		runConsumer(rabbitURL)
		slog.Warn("Worker connection dropped, retrying in 5s...")
		time.Sleep(5 * time.Second)
	}
}

func runConsumer(rabbitURL string) {
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		slog.Error("Worker failed to connect to RabbitMQ", "error", err)
		return
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		slog.Error("Worker failed to open a channel", "error", err)
		return
	}
	defer ch.Close()

	err = ch.ExchangeDeclare("dlx", "direct", true, false, false, false, nil)
	if err != nil {
		slog.Error("Worker failed to declare DLX", "error", err)
		return
	}

	_, err = ch.QueueDeclare("ticket_emails_dlq", true, false, false, false, nil)
	if err != nil {
		slog.Error("Worker failed to declare DLQ", "error", err)
		return
	}

	err = ch.QueueBind("ticket_emails_dlq", "ticket_emails", "dlx", false, nil)
	if err != nil {
		slog.Error("Worker failed to bind DLQ", "error", err)
		return
	}

	args := amqp.Table{
		"x-dead-letter-exchange":    "dlx",
		"x-dead-letter-routing-key": "ticket_emails",
	}
	q, err := ch.QueueDeclare("ticket_emails", true, false, false, false, args)
	if err != nil {
		slog.Error("Worker failed to declare queue", "error", err)
		return
	}

	err = ch.Qos(1, 0, false)
	if err != nil {
		slog.Error("Failed to set QoS", "error", err)
		return
	}

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		slog.Error("Failed to register a consumer", "error", err)
		return
	}

	go func() {
		for d := range msgs {
			var payload EmailPayload
			err := json.Unmarshal(d.Body, &payload)
			if err != nil {
				slog.Error("Error decoding JSON", "error", err)
				d.Nack(false, false)
				continue
			}

			slog.Info("Sending email...", "email", payload.Email)

			smtpHost := "mailpit"
			smtpPort := "1025"    

			var auth smtp.Auth = nil

			subject := "Subject: Your TookMyShow Ticket Confirmed!\n"
			contentType := "MIME-version: 1.0;\nContent-Type: text/html; charset=\"UTF-8\";\n\n"

			body := fmt.Sprintf(`
				<h2>Ticket Confirmation</h2>
				<p>Thank you for your purchase!</p>
				<p><strong>Account:</strong> %s</p>
				<p><strong>Seat ID:</strong> %s</p>
				<p>Enjoy the show!</p>
			`, payload.Email, payload.SeatID)

			msg := []byte(subject + contentType + body)

	
			err = smtp.SendMail(smtpHost+":"+smtpPort, auth, "noreply@tookmyshow.com", []string{payload.Email}, msg)
			if err != nil {
				slog.Error("Failed to send email", "email", payload.Email, "error", err)

				d.Nack(false, false) // Rejects and sends to DLQ
				continue
			}

			slog.Info("Successfully delivered ticket", "email", payload.Email)

			d.Ack(false)
		}
	}()

	slog.Info("Email Worker initialized. Waiting for messages...")
	
	closeErr := <-conn.NotifyClose(make(chan *amqp.Error))
	slog.Warn("Email Worker connection closed", "error", closeErr)
}
