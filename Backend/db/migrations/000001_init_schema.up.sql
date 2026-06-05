-- Required for UUID generation
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ENUMs
CREATE TYPE seat_status AS ENUM ('available', 'locked', 'sold', 'blocked');
CREATE TYPE payment_status AS ENUM ('pending', 'processing', 'succeeded', 'failed', 'refunded');
CREATE TYPE booking_status AS ENUM ('pending','confirmed','failed','expired');

-- Core tables
CREATE TABLE organizer (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL
);

CREATE TABLE venue (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    timezone VARCHAR(50) NOT NULL DEFAULT 'UTC'
);

CREATE TABLE event (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organizer_id UUID NOT NULL REFERENCES organizer(id),
    name VARCHAR(255) NOT NULL
);

CREATE TABLE event_instance (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id UUID NOT NULL REFERENCES event(id) ON DELETE CASCADE,
    venue_id UUID NOT NULL REFERENCES venue(id),
    start_time TIMESTAMP WITH TIME ZONE NOT NULL
);

-- Seats
CREATE TABLE seat (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    venue_id UUID NOT NULL REFERENCES venue(id) ON DELETE CASCADE,
    section VARCHAR(50) NOT NULL,
    row_name VARCHAR(10),
    seat_number INT,
    UNIQUE(venue_id, section, row_name, seat_number)
);

-- Zones
CREATE TABLE event_zone (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_instance_id UUID NOT NULL REFERENCES event_instance(id) ON DELETE CASCADE,
    name VARCHAR(50) NOT NULL,
    is_general_admission BOOLEAN DEFAULT FALSE,
    total_capacity INT,
    available_capacity INT,
    CHECK (available_capacity >= 0),
    CHECK (
        (is_general_admission = true AND total_capacity IS NOT NULL AND total_capacity > 0)
        OR (is_general_admission = false)
    ),
    UNIQUE(event_instance_id, name)
);

-- Pricing
CREATE TABLE price_tier (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_zone_id UUID NOT NULL REFERENCES event_zone(id) ON DELETE CASCADE,
    name VARCHAR(50) NOT NULL,
    price DECIMAL(10, 2) NOT NULL,
    UNIQUE(event_zone_id, name)
);

-- Event seat (availability)
CREATE TABLE event_seat (
    event_instance_id UUID NOT NULL REFERENCES event_instance(id) ON DELETE CASCADE,
    seat_id UUID NOT NULL REFERENCES seat(id),
    event_zone_id UUID NOT NULL REFERENCES event_zone(id),
    status seat_status DEFAULT 'available',
    version INT DEFAULT 1,
    PRIMARY KEY (event_instance_id, seat_id)
);

-- Booking (FIXED: status included here)
CREATE TABLE booking (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_email VARCHAR(255) NOT NULL,
    total_amount DECIMAL(10, 2) NOT NULL,
    idempotency_key VARCHAR(255) UNIQUE,
    status booking_status DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Payment
CREATE TABLE payment (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES booking(id) ON DELETE CASCADE,
    provider VARCHAR(50) NOT NULL,
    provider_transaction_id VARCHAR(255),
    amount DECIMAL(10, 2) NOT NULL,
    status payment_status DEFAULT 'pending',
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Partitioned ticket table
CREATE TABLE ticket (
    id UUID DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL,
    event_instance_id UUID NOT NULL,
    seat_id UUID,
    price_paid DECIMAL(10, 2) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    PRIMARY KEY (id, created_at),
    UNIQUE(event_instance_id, seat_id, created_at)
) PARTITION BY RANGE (created_at);

CREATE TABLE ticket_2026 PARTITION OF ticket
FOR VALUES FROM ('2026-01-01') TO ('2027-01-01');

-- Audit log
CREATE TABLE audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_name VARCHAR(50) NOT NULL,
    entity_id UUID NOT NULL,
    action VARCHAR(50) NOT NULL,
    old_value JSONB,
    new_value JSONB,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_event_seat_status 
ON event_seat(event_instance_id, status);

CREATE INDEX idx_event_zone_instance 
ON event_zone(event_instance_id);

CREATE INDEX idx_payment_booking 
ON payment(booking_id);

CREATE INDEX idx_ticket_event_instance 
ON ticket(event_instance_id);


-- 1. Organizer
INSERT INTO organizer (id, name) 
VALUES ('11111111-1111-1111-1111-111111111111', 'Live Nation');

-- 2. Venue
INSERT INTO venue (id, name, timezone) 
VALUES ('22222222-2222-2222-2222-222222222222', 'Madison Square Garden', 'America/New_York');

-- 3. The Abstract Event
INSERT INTO event (id, organizer_id, name) 
VALUES ('33333333-3333-3333-3333-333333333333', '11111111-1111-1111-1111-111111111111', 'The Eras Tour');

-- 4. The Event Instance (The specific showtime you will hit in Postman)
INSERT INTO event_instance (id, event_id, venue_id, start_time) 
VALUES ('44444444-4444-4444-4444-444444444444', '33333333-3333-3333-3333-333333333333', '22222222-2222-2222-2222-222222222222', '2026-08-01 19:00:00+00');

-- 5. The Physical Seats (Belong to the Venue)
INSERT INTO seat (id, venue_id, section, row_name, seat_number) VALUES 
('66666666-6666-6666-6666-666666666661', '22222222-2222-2222-2222-222222222222', 'Floor', 'A', 1),
('66666666-6666-6666-6666-666666666662', '22222222-2222-2222-2222-222222222222', 'Floor', 'A', 2);

-- 6. The Event Zone and Pricing
INSERT INTO event_zone (id, event_instance_id, name, is_general_admission) 
VALUES ('55555555-5555-5555-5555-555555555555', '44444444-4444-4444-4444-444444444444', 'VIP Floor', false);

INSERT INTO price_tier (event_zone_id, name, price) 
VALUES ('55555555-5555-5555-5555-555555555555', 'Standard', 250.00);

-- 7. Materialized Availability (The intersection of the showtime and the physical seats)
INSERT INTO event_seat (event_instance_id, seat_id, event_zone_id, status) VALUES 
('44444444-4444-4444-4444-444444444444', '66666666-6666-6666-6666-666666666661', '55555555-5555-5555-5555-555555555555', 'available'),
('44444444-4444-4444-4444-444444444444', '66666666-6666-6666-6666-666666666662', '55555555-5555-5555-5555-555555555555', 'available');