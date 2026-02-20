package models

import (
	"time"

	"github.com/google/uuid"
)

type UserRole string

const (
	UserRoleStudent   UserRole = "STUDENT"
	UserRoleLibrarian UserRole = "LIBRARIAN"
)

type BookCopyStatus string

const (
	BookCopyStatusAvailable  BookCopyStatus = "AVAILABLE"
	BookCopyStatusCheckedOut BookCopyStatus = "CHECKED_OUT"
)

type User struct {
	ID   uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Name string    `gorm:"size:255;not null" json:"name"`
	Role UserRole  `gorm:"type:user_role;not null" json:"role"`
}

type Book struct {
	ID          uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	Title       string    `gorm:"size:255;not null" json:"title"`
	Author      string    `gorm:"size:255;not null" json:"author"`
	TotalCopies int       `gorm:"not null" json:"total_copies"`
}

type BookCopy struct {
	ID     uuid.UUID      `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	BookID uuid.UUID      `gorm:"type:uuid;not null;index" json:"book_id"`
	Book   Book           `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	Status BookCopyStatus `gorm:"type:book_copy_status;not null;index" json:"status"`
}

type Checkout struct {
	ID          uuid.UUID  `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	BookCopyID  uuid.UUID  `gorm:"type:uuid;not null;index" json:"book_copy_id"`
	BookCopy    BookCopy   `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	UserID      uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	User        User       `gorm:"constraint:OnUpdate:CASCADE,OnDelete:RESTRICT;" json:"-"`
	CheckoutAt  time.Time  `gorm:"not null" json:"checkout_date"`
	DueDate     time.Time  `gorm:"not null" json:"due_date"`
	ReturnedAt  *time.Time `json:"returned_at"`
	FineAmount  int        `gorm:"not null;default:0" json:"fine_amount"`
}

type Reservation struct {
	ID            uuid.UUID `gorm:"type:uuid;default:uuid_generate_v4();primaryKey" json:"id"`
	BookID        uuid.UUID `gorm:"type:uuid;not null;index" json:"book_id"`
	Book          Book      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	UserID        uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	User          User      `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
	QueuePosition int       `gorm:"not null;index" json:"queue_position"`
	CreatedAt     time.Time `gorm:"not null;default:now()" json:"created_at"`
}

