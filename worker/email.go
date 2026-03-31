package workers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/smtp"

	amqp "github.com/rabbitmq/amqp091-go"
)

type EmailPayload struct {
	Email  string `json:"email"`
	SeatID string `json:"seat_id"`
}

func StartEmailConsumer(rabbitURL string) {
	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		log.Fatalf("Worker failed to connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("Worker failed to open a channel: %v", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare("ticket_emails", true, false, false, false, nil)
	if err != nil {
		log.Fatalf("Worker failed to declare queue: %v", err)
	}

	err = ch.Qos(1, 0, false)
	if err != nil {
		log.Fatalf("Failed to set QoS: %v", err)
	}

	msgs, err := ch.Consume(q.Name, "", false, false, false, false, nil)
	if err != nil {
		log.Fatalf("Failed to register a consumer: %v", err)
	}

	forever := make(chan bool)

	go func() {
		for d := range msgs {
			var payload EmailPayload
			err := json.Unmarshal(d.Body, &payload)
			if err != nil {
				log.Printf("Error decoding JSON: %v", err)
				d.Nack(false, false)
				continue
			}

			log.Printf("[x] Sending email to: %s...", payload.Email)

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
				log.Printf("Failed to send email to %s: %v\n", payload.Email, err)

				d.Nack(false, true)
				continue
			}

			log.Printf("[v] Successfully delivered ticket to %s!\n", payload.Email)

			d.Ack(false)
		}
	}()

	log.Println(" [*] Email Worker initialized. Waiting for messages...")
	<-forever
}
