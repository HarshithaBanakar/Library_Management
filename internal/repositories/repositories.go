package repositories

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"library/internal/models"
)

type UserRepository interface {
	GetByID(db *gorm.DB, id uuid.UUID) (*models.User, error)
}

type BookRepository interface {
	Create(db *gorm.DB, book *models.Book) error
	List(db *gorm.DB) ([]models.Book, error)
	GetByID(db *gorm.DB, id uuid.UUID) (*models.Book, error)
	IncrementTotalCopies(db *gorm.DB, bookID uuid.UUID, delta int) error
}

type BookCopyRepository interface {
	Create(db *gorm.DB, copy *models.BookCopy) error
	FindAvailableForUpdate(db *gorm.DB, bookID uuid.UUID) (*models.BookCopy, error)
	UpdateStatus(db *gorm.DB, id uuid.UUID, status models.BookCopyStatus) error
}

type CheckoutRepository interface {
	Create(db *gorm.DB, checkout *models.Checkout) error
	MarkReturned(db *gorm.DB, checkoutID uuid.UUID, returnedAt time.Time, fineAmount int) error
	GetByIDForUpdate(db *gorm.DB, id uuid.UUID) (*models.Checkout, error)
	ListByUser(db *gorm.DB, userID uuid.UUID) ([]models.Checkout, error)
}

type ReservationRepository interface {
	Create(db *gorm.DB, reservation *models.Reservation) error
	GetNextForBook(db *gorm.DB, bookID uuid.UUID) (*models.Reservation, error)
	GetByBookAndUser(db *gorm.DB, bookID, userID uuid.UUID) (*models.Reservation, error)
	Delete(db *gorm.DB, id uuid.UUID) error
	GetNextQueuePosition(db *gorm.DB, bookID uuid.UUID) (int, error)
	ListByBook(db *gorm.DB, bookID uuid.UUID) ([]models.Reservation, error)
}

// concrete implementations

type userRepository struct {
	db *gorm.DB
}

func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

func (r *userRepository) GetByID(db *gorm.DB, id uuid.UUID) (*models.User, error) {
	if db == nil {
		db = r.db
	}
	var user models.User
	if err := db.First(&user, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

type bookRepository struct {
	db *gorm.DB
}

func NewBookRepository(db *gorm.DB) BookRepository {
	return &bookRepository{db: db}
}

func (r *bookRepository) Create(db *gorm.DB, book *models.Book) error {
	if db == nil {
		db = r.db
	}
	return db.Create(book).Error
}

func (r *bookRepository) List(db *gorm.DB) ([]models.Book, error) {
	if db == nil {
		db = r.db
	}
	var books []models.Book
	if err := db.Find(&books).Error; err != nil {
		return nil, err
	}
	return books, nil
}

func (r *bookRepository) GetByID(db *gorm.DB, id uuid.UUID) (*models.Book, error) {
	if db == nil {
		db = r.db
	}
	var book models.Book
	if err := db.First(&book, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &book, nil
}

func (r *bookRepository) IncrementTotalCopies(db *gorm.DB, bookID uuid.UUID, delta int) error {
	if db == nil {
		db = r.db
	}
	return db.Model(&models.Book{}).
		Where("id = ?", bookID).
		UpdateColumn("total_copies", gorm.Expr("total_copies + ?", delta)).
		Error
}

type bookCopyRepository struct {
	db *gorm.DB
}

func NewBookCopyRepository(db *gorm.DB) BookCopyRepository {
	return &bookCopyRepository{db: db}
}

func (r *bookCopyRepository) Create(db *gorm.DB, copy *models.BookCopy) error {
	if db == nil {
		db = r.db
	}
	return db.Create(copy).Error
}

func (r *bookCopyRepository) FindAvailableForUpdate(db *gorm.DB, bookID uuid.UUID) (*models.BookCopy, error) {
	if db == nil {
		db = r.db
	}
	var copy models.BookCopy
	err := db.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("book_id = ? AND status = ?", bookID, models.BookCopyStatusAvailable).
		Order("id").
		First(&copy).Error
	if err != nil {
		return nil, err
	}
	return &copy, nil
}

func (r *bookCopyRepository) UpdateStatus(db *gorm.DB, id uuid.UUID, status models.BookCopyStatus) error {
	if db == nil {
		db = r.db
	}
	return db.Model(&models.BookCopy{}).
		Where("id = ?", id).
		Update("status", status).
		Error
}

type checkoutRepository struct {
	db *gorm.DB
}

func NewCheckoutRepository(db *gorm.DB) CheckoutRepository {
	return &checkoutRepository{db: db}
}

func (r *checkoutRepository) Create(db *gorm.DB, checkout *models.Checkout) error {
	if db == nil {
		db = r.db
	}
	return db.Create(checkout).Error
}

func (r *checkoutRepository) MarkReturned(db *gorm.DB, checkoutID uuid.UUID, returnedAt time.Time, fineAmount int) error {
	if db == nil {
		db = r.db
	}
	return db.Model(&models.Checkout{}).
		Where("id = ? AND returned_at IS NULL", checkoutID).
		Updates(map[string]interface{}{
			"returned_at": returnedAt,
			"fine_amount": fineAmount,
		}).Error
}

func (r *checkoutRepository) GetByIDForUpdate(db *gorm.DB, id uuid.UUID) (*models.Checkout, error) {
	if db == nil {
		db = r.db
	}
	var checkout models.Checkout
	err := db.
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Preload("BookCopy").
		First(&checkout, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &checkout, nil
}

func (r *checkoutRepository) ListByUser(db *gorm.DB, userID uuid.UUID) ([]models.Checkout, error) {
	if db == nil {
		db = r.db
	}
	var checkouts []models.Checkout
	if err := db.Where("user_id = ?", userID).Find(&checkouts).Error; err != nil {
		return nil, err
	}
	return checkouts, nil
}

type reservationRepository struct {
	db *gorm.DB
}

func NewReservationRepository(db *gorm.DB) ReservationRepository {
	return &reservationRepository{db: db}
}

func (r *reservationRepository) Create(db *gorm.DB, reservation *models.Reservation) error {
	if db == nil {
		db = r.db
	}
	return db.Create(reservation).Error
}

func (r *reservationRepository) GetNextForBook(db *gorm.DB, bookID uuid.UUID) (*models.Reservation, error) {
	if db == nil {
		db = r.db
	}
	var res models.Reservation
	err := db.Where("book_id = ?", bookID).
		Order("queue_position ASC, created_at ASC").
		First(&res).Error
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (r *reservationRepository) GetByBookAndUser(db *gorm.DB, bookID, userID uuid.UUID) (*models.Reservation, error) {
	if db == nil {
		db = r.db
	}
	var res models.Reservation
	err := db.Where("book_id = ? AND user_id = ?", bookID, userID).First(&res).Error
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (r *reservationRepository) Delete(db *gorm.DB, id uuid.UUID) error {
	if db == nil {
		db = r.db
	}
	return db.Delete(&models.Reservation{}, "id = ?", id).Error
}

func (r *reservationRepository) GetNextQueuePosition(db *gorm.DB, bookID uuid.UUID) (int, error) {
	if db == nil {
		db = r.db
	}
	// Lock reservation rows for this book so MAX(queue_position) is stable under concurrency.
	var ids []uuid.UUID
	if err := db.Model(&models.Reservation{}).
		Where("book_id = ?", bookID).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	var maxPos int
	if err := db.Model(&models.Reservation{}).
		Where("book_id = ?", bookID).
		Select("COALESCE(MAX(queue_position), 0)").
		Scan(&maxPos).Error; err != nil {
		return 0, err
	}
	return maxPos + 1, nil
}

func (r *reservationRepository) ListByBook(db *gorm.DB, bookID uuid.UUID) ([]models.Reservation, error) {
	if db == nil {
		db = r.db
	}
	var res []models.Reservation
	if err := db.Where("book_id = ?", bookID).
		Order("queue_position ASC").
		Find(&res).Error; err != nil {
		return nil, err
	}
	return res, nil
}

