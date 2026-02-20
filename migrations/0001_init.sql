-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Enums
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'user_role') THEN
        CREATE TYPE user_role AS ENUM ('STUDENT', 'LIBRARIAN');
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'book_copy_status') THEN
        CREATE TYPE book_copy_status AS ENUM ('AVAILABLE', 'CHECKED_OUT');
    END IF;
END$$;

-- Users
CREATE TABLE IF NOT EXISTS users (
    id   UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    role user_role    NOT NULL
);

-- Books
CREATE TABLE IF NOT EXISTS books (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title        VARCHAR(255) NOT NULL,
    author       VARCHAR(255) NOT NULL,
    total_copies INT          NOT NULL
);

-- BookCopies
CREATE TABLE IF NOT EXISTS book_copies (
    id      UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    book_id UUID             NOT NULL REFERENCES books(id) ON UPDATE CASCADE ON DELETE CASCADE,
    status  book_copy_status NOT NULL,
    CONSTRAINT book_copies_status_check CHECK (status IN ('AVAILABLE', 'CHECKED_OUT'))
);
CREATE INDEX IF NOT EXISTS idx_book_copies_book_id ON book_copies(book_id);
CREATE INDEX IF NOT EXISTS idx_book_copies_status ON book_copies(status);

-- Checkouts
CREATE TABLE IF NOT EXISTS checkouts (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    book_copy_id UUID      NOT NULL REFERENCES book_copies(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    user_id      UUID      NOT NULL REFERENCES users(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    checkout_at  TIMESTAMP NOT NULL,
    due_date     TIMESTAMP NOT NULL,
    returned_at  TIMESTAMP NULL,
    fine_amount  INT       NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_checkouts_book_copy_id ON checkouts(book_copy_id);
CREATE INDEX IF NOT EXISTS idx_checkouts_user_id ON checkouts(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_active_checkout ON checkouts(book_copy_id) WHERE returned_at IS NULL;

-- Reservations
CREATE TABLE IF NOT EXISTS reservations (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    book_id        UUID      NOT NULL REFERENCES books(id) ON UPDATE CASCADE ON DELETE CASCADE,
    user_id        UUID      NOT NULL REFERENCES users(id) ON UPDATE CASCADE ON DELETE CASCADE,
    queue_position INT       NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_reservations_book_id ON reservations(book_id);
CREATE INDEX IF NOT EXISTS idx_reservations_user_id ON reservations(user_id);
CREATE INDEX IF NOT EXISTS idx_reservations_queue ON reservations(book_id, queue_position);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_user_book_reservation ON reservations(book_id, user_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_book_queue_position ON reservations(book_id, queue_position);

