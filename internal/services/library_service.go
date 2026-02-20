package services

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"library/internal/models"
	"library/internal/repositories"
)

var (
	ErrNoAvailableCopy        = errors.New("no available copies, reservation created")
	ErrCheckoutAlreadyReturned = errors.New("checkout already returned")
)

type LibraryService interface {
	CreateBook(title, author string, totalCopies int) (*models.Book, error)
	AddBookCopy(bookID uuid.UUID) (*models.BookCopy, error)
	ListBooks() ([]models.Book, error)

	CheckoutBook(bookID, userID uuid.UUID) (*models.Checkout, *models.Reservation, error)
	ReturnCheckout(checkoutID uuid.UUID) (*models.Checkout, error)

	ListUserCheckouts(userID uuid.UUID) ([]models.Checkout, error)
	ListReservationsForBook(bookID uuid.UUID) ([]models.Reservation, error)
}

type libraryService struct {
	db              *gorm.DB
	userRepo        repositories.UserRepository
	bookRepo        repositories.BookRepository
	bookCopyRepo    repositories.BookCopyRepository
	checkoutRepo    repositories.CheckoutRepository
	reservationRepo repositories.ReservationRepository
}

func NewLibraryService(
	db *gorm.DB,
	userRepo repositories.UserRepository,
	bookRepo repositories.BookRepository,
	bookCopyRepo repositories.BookCopyRepository,
	checkoutRepo repositories.CheckoutRepository,
	reservationRepo repositories.ReservationRepository,
) LibraryService {
	return &libraryService{
		db:              db,
		userRepo:        userRepo,
		bookRepo:        bookRepo,
		bookCopyRepo:    bookCopyRepo,
		checkoutRepo:    checkoutRepo,
		reservationRepo: reservationRepo,
	}
}

func (s *libraryService) CreateBook(title, author string, totalCopies int) (*models.Book, error) {
	book := &models.Book{
		Title:       title,
		Author:      author,
		TotalCopies: 0,
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.bookRepo.Create(tx, book); err != nil {
			return err
		}
		for i := 0; i < totalCopies; i++ {
			copy := &models.BookCopy{
				BookID: book.ID,
				Status: models.BookCopyStatusAvailable,
			}
			if err := s.bookCopyRepo.Create(tx, copy); err != nil {
				return err
			}
		}
		if err := s.bookRepo.IncrementTotalCopies(tx, book.ID, totalCopies); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	book.TotalCopies = totalCopies
	return book, nil
}

func (s *libraryService) AddBookCopy(bookID uuid.UUID) (*models.BookCopy, error) {
	copy := &models.BookCopy{
		BookID: bookID,
		Status: models.BookCopyStatusAvailable,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.bookRepo.GetByID(tx, bookID); err != nil {
			return err
		}
		if err := s.bookCopyRepo.Create(tx, copy); err != nil {
			return err
		}
		if err := s.bookRepo.IncrementTotalCopies(tx, bookID, 1); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return copy, nil
}

func (s *libraryService) ListBooks() ([]models.Book, error) {
	return s.bookRepo.List(nil)
}

// CheckoutBook implements the transactional checkout flow.
// It either returns a Checkout (if copy available) or a Reservation (if queued).
func (s *libraryService) CheckoutBook(bookID, userID uuid.UUID) (*models.Checkout, *models.Reservation, error) {
	var resultCheckout *models.Checkout
	var resultReservation *models.Reservation

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if _, err := s.userRepo.GetByID(tx, userID); err != nil {
			return err
		}

		// Try to lock an available copy.
		copy, err := s.bookCopyRepo.FindAvailableForUpdate(tx, bookID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// No copies: return existing reservation if user already reserved this book.
				existing, err := s.reservationRepo.GetByBookAndUser(tx, bookID, userID)
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if existing != nil {
					resultReservation = existing
					return ErrNoAvailableCopy
				}
				// Create new reservation (with optional retry on duplicate queue_position).
				res, err := s.createReservationWithRetry(tx, bookID, userID)
				if err != nil {
					return err
				}
				resultReservation = res
				return ErrNoAvailableCopy
			}
			return err
		}

		// Mark copy as checked out.
		if err := s.bookCopyRepo.UpdateStatus(tx, copy.ID, models.BookCopyStatusCheckedOut); err != nil {
			return err
		}

		now := time.Now().UTC()
		due := now.AddDate(0, 0, 14) // 14 days loan duration

		checkout := &models.Checkout{
			BookCopyID: copy.ID,
			UserID:     userID,
			CheckoutAt: now,
			DueDate:    due,
			FineAmount: 0,
		}
		if err := s.checkoutRepo.Create(tx, checkout); err != nil {
			return err
		}
		resultCheckout = checkout
		return nil
	})

	if err != nil {
		if errors.Is(err, ErrNoAvailableCopy) {
			return nil, resultReservation, nil
		}
		return nil, nil, err
	}
	return resultCheckout, nil, nil
}

// ReturnCheckout implements the transactional return flow.
func (s *libraryService) ReturnCheckout(checkoutID uuid.UUID) (*models.Checkout, error) {
	var updated *models.Checkout
	err := s.db.Transaction(func(tx *gorm.DB) error {
		checkout, err := s.checkoutRepo.GetByIDForUpdate(tx, checkoutID)
		if err != nil {
			return err
		}
		if checkout.ReturnedAt != nil {
			return ErrCheckoutAlreadyReturned
		}

		now := time.Now().UTC()
		fine := calculateFine(checkout.DueDate, now)

		if err := s.checkoutRepo.MarkReturned(tx, checkout.ID, now, fine); err != nil {
			return err
		}

		// Release copy.
		if err := s.bookCopyRepo.UpdateStatus(tx, checkout.BookCopyID, models.BookCopyStatusAvailable); err != nil {
			return err
		}

		// Check reservations for the book of this copy.
		bookID := checkout.BookCopy.BookID
		res, err := s.reservationRepo.GetNextForBook(tx, bookID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if err == nil && res != nil {
			// Immediately assign this copy to reserved user.
			if err := s.bookCopyRepo.UpdateStatus(tx, checkout.BookCopyID, models.BookCopyStatusCheckedOut); err != nil {
				return err
			}
			if err := s.reservationRepo.Delete(tx, res.ID); err != nil {
				return err
			}

			now2 := time.Now().UTC()
			due2 := now2.AddDate(0, 0, 14)
			newCheckout := &models.Checkout{
				BookCopyID: checkout.BookCopyID,
				UserID:     res.UserID,
				CheckoutAt: now2,
				DueDate:    due2,
				FineAmount: 0,
			}
			if err := s.checkoutRepo.Create(tx, newCheckout); err != nil {
				return err
			}
		}

		// Reload updated checkout.
		reloaded, err := s.checkoutRepo.GetByIDForUpdate(tx, checkoutID)
		if err != nil {
			return err
		}
		updated = reloaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// createReservationWithRetry creates a reservation; on duplicate queue_position (unique constraint), retries once.
func (s *libraryService) createReservationWithRetry(tx *gorm.DB, bookID, userID uuid.UUID) (*models.Reservation, error) {
	nextPos, err := s.reservationRepo.GetNextQueuePosition(tx, bookID)
	if err != nil {
		return nil, err
	}
	res := &models.Reservation{
		BookID:        bookID,
		UserID:        userID,
		QueuePosition: nextPos,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.reservationRepo.Create(tx, res); err != nil {
		if isUniqueViolation(err) {
			// Retry once with a fresh position (another concurrent insert took our slot).
			nextPos, err = s.reservationRepo.GetNextQueuePosition(tx, bookID)
			if err != nil {
				return nil, err
			}
			res = &models.Reservation{
				BookID:        bookID,
				UserID:        userID,
				QueuePosition: nextPos,
				CreatedAt:     time.Now().UTC(),
			}
			if err := s.reservationRepo.Create(tx, res); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	return res, nil
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

func calculateFine(dueDate, returnedAt time.Time) int {
	if !returnedAt.After(dueDate) {
		return 0
	}
	diff := returnedAt.Truncate(24 * time.Hour).Sub(dueDate.Truncate(24 * time.Hour))
	daysLate := int(diff.Hours() / 24)
	if daysLate <= 0 {
		daysLate = 1
	}
	return daysLate * 10
}

func (s *libraryService) ListUserCheckouts(userID uuid.UUID) ([]models.Checkout, error) {
	return s.checkoutRepo.ListByUser(nil, userID)
}

func (s *libraryService) ListReservationsForBook(bookID uuid.UUID) ([]models.Reservation, error) {
	return s.reservationRepo.ListByBook(nil, bookID)
}

