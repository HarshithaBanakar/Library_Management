package services

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"library/internal/models"
	"library/internal/repositories"
)

// ─── Fine Calculation Constants ───────────────────────────────────────────────

const (
	// LoanPeriodDays is the number of days a user may keep a book before incurring fines.
	LoanPeriodDays = 14

	// FinePerDay is the fine amount (in currency units) charged per day overdue.
	// Minimum charged is 1 day (i.e. FinePerDay) even if returned less than 24 h late.
	FinePerDay = 10
)

// ─── Sentinel Errors ──────────────────────────────────────────────────────────

var (
	// ErrNoAvailableCopy is returned (internally) when all copies of a book are checked
	// out. It signals CheckoutBook to create a reservation instead.
	ErrNoAvailableCopy = errors.New("no available copies, reservation created")

	// ErrCheckoutAlreadyReturned is returned when a return is attempted on a checkout
	// that has already been marked returned.
	ErrCheckoutAlreadyReturned = errors.New("checkout already returned")

	// ErrDuplicateReservation is returned when the user already has an active
	// reservation for the same book.
	ErrDuplicateReservation = errors.New("user already has an active reservation for this book")

	// ErrAlreadyCheckedOut is returned when the user attempts to check out a copy
	// that is already checked out (should not normally occur given DB constraints).
	ErrAlreadyCheckedOut = errors.New("book copy is already checked out")

	// ErrBookNotFound is returned when the requested book does not exist.
	ErrBookNotFound = errors.New("book not found")

	// ErrUserNotFound is returned when the referenced user does not exist.
	ErrUserNotFound = errors.New("user not found")

	// ErrCheckoutNotFound is returned when the referenced checkout does not exist.
	ErrCheckoutNotFound = errors.New("checkout not found")
)

// ─── Service Interface ────────────────────────────────────────────────────────

// LibraryService defines the application-level operations of the library system.
type LibraryService interface {
	CreateBook(title, author string, totalCopies int) (*models.Book, error)
	AddBookCopy(bookID uuid.UUID) (*models.BookCopy, error)
	ListBooks() ([]models.Book, error)

	CheckoutBook(bookID, userID uuid.UUID) (*models.Checkout, *models.Reservation, error)
	ReturnCheckout(checkoutID uuid.UUID) (*models.Checkout, error)

	ListUserCheckouts(userID uuid.UUID) ([]models.Checkout, error)
	ListReservationsForBook(bookID uuid.UUID) ([]models.Reservation, error)
}

// ─── Implementation ───────────────────────────────────────────────────────────

type libraryService struct {
	db              *gorm.DB
	userRepo        repositories.UserRepository
	bookRepo        repositories.BookRepository
	bookCopyRepo    repositories.BookCopyRepository
	checkoutRepo    repositories.CheckoutRepository
	reservationRepo repositories.ReservationRepository
}

// NewLibraryService wires up all dependencies and returns a LibraryService.
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

// ─── Book Management ──────────────────────────────────────────────────────────

// CreateBook creates a book record together with the requested number of physical copies,
// all within a single transaction.
func (s *libraryService) CreateBook(title, author string, totalCopies int) (*models.Book, error) {
	book := &models.Book{
		Title:       title,
		Author:      author,
		TotalCopies: 0,
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.bookRepo.Create(tx, book); err != nil {
			log.Printf("[ERROR] CreateBook: failed to create book record: %v", err)
			return err
		}
		for i := 0; i < totalCopies; i++ {
			copy := &models.BookCopy{
				BookID: book.ID,
				Status: models.BookCopyStatusAvailable,
			}
			if err := s.bookCopyRepo.Create(tx, copy); err != nil {
				log.Printf("[ERROR] CreateBook: failed to create book copy %d: %v", i+1, err)
				return err
			}
		}
		if err := s.bookRepo.IncrementTotalCopies(tx, book.ID, totalCopies); err != nil {
			log.Printf("[ERROR] CreateBook: failed to increment total_copies: %v", err)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	book.TotalCopies = totalCopies
	log.Printf("[INFO] CreateBook: created book %q (id=%s) with %d copies", book.Title, book.ID, totalCopies)
	return book, nil
}

// AddBookCopy adds a single physical copy to an existing book, updating total_copies atomically.
func (s *libraryService) AddBookCopy(bookID uuid.UUID) (*models.BookCopy, error) {
	// Validate book exists before opening a transaction.
	if _, err := s.bookRepo.GetByID(nil, bookID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBookNotFound
		}
		return nil, err
	}

	copy := &models.BookCopy{
		BookID: bookID,
		Status: models.BookCopyStatusAvailable,
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.bookCopyRepo.Create(tx, copy); err != nil {
			log.Printf("[ERROR] AddBookCopy: failed to create copy for book %s: %v", bookID, err)
			return err
		}
		if err := s.bookRepo.IncrementTotalCopies(tx, bookID, 1); err != nil {
			log.Printf("[ERROR] AddBookCopy: failed to increment total_copies for book %s: %v", bookID, err)
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.Printf("[INFO] AddBookCopy: added copy %s for book %s", copy.ID, bookID)
	return copy, nil
}

// ListBooks returns all books in the catalogue.
func (s *libraryService) ListBooks() ([]models.Book, error) {
	return s.bookRepo.List(nil)
}

// ─── Checkout ─────────────────────────────────────────────────────────────────

// CheckoutBook implements the transactional checkout flow.
//
// Happy path: an available copy exists → it is locked (SELECT FOR UPDATE), marked
// CHECKED_OUT, and a Checkout record is created (14-day loan period).
//
// No-copy path: all copies are out → a Reservation is inserted in the queue.
// Returns (checkout, nil, nil) or (nil, reservation, nil). Any other error is surfaced
// as (nil, nil, err).
func (s *libraryService) CheckoutBook(bookID, userID uuid.UUID) (*models.Checkout, *models.Reservation, error) {
	var resultCheckout *models.Checkout
	var resultReservation *models.Reservation

	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 1. Validate user exists.
		if _, err := s.userRepo.GetByID(tx, userID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserNotFound
			}
			return err
		}

		// 2. Validate book exists.
		if _, err := s.bookRepo.GetByID(tx, bookID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrBookNotFound
			}
			return err
		}

		// 3. Try to lock an available copy (SELECT … FOR UPDATE).
		copy, err := s.bookCopyRepo.FindAvailableForUpdate(tx, bookID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// No copies available — fall through to reservation logic.
				log.Printf("[INFO] CheckoutBook: no available copies for book %s, checking reservations for user %s", bookID, userID)

				// Check if user already has a reservation for this book.
				existing, err := s.reservationRepo.GetByBookAndUser(tx, bookID, userID)
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if existing != nil {
					log.Printf("[WARN] CheckoutBook: user %s already has reservation %s for book %s", userID, existing.ID, bookID)
					return ErrDuplicateReservation
				}

				// Create a new reservation with retry on queue_position collision.
				res, err := s.createReservationWithRetry(tx, bookID, userID)
				if err != nil {
					log.Printf("[ERROR] CheckoutBook: failed to create reservation for user %s / book %s: %v", userID, bookID, err)
					return err
				}
				log.Printf("[INFO] CheckoutBook: reservation created for user %s / book %s at queue position %d (id=%s)", userID, bookID, res.QueuePosition, res.ID)
				resultReservation = res
				return ErrNoAvailableCopy
			}
			return err
		}

		// 4. Mark copy as CHECKED_OUT.
		if err := s.bookCopyRepo.UpdateStatus(tx, copy.ID, models.BookCopyStatusCheckedOut); err != nil {
			log.Printf("[ERROR] CheckoutBook: failed to mark copy %s as CHECKED_OUT: %v", copy.ID, err)
			return err
		}

		// 5. Create the Checkout record.
		now := time.Now().UTC()
		due := now.AddDate(0, 0, LoanPeriodDays)

		checkout := &models.Checkout{
			BookCopyID: copy.ID,
			UserID:     userID,
			CheckoutAt: now,
			DueDate:    due,
			FineAmount: 0,
		}
		if err := s.checkoutRepo.Create(tx, checkout); err != nil {
			log.Printf("[ERROR] CheckoutBook: failed to create checkout record: %v", err)
			return err
		}
		resultCheckout = checkout
		log.Printf("[INFO] CheckoutBook: checkout created (id=%s) for user %s / copy %s, due %s", checkout.ID, userID, copy.ID, due.Format("2006-01-02"))
		return nil
	})

	if err != nil {
		if errors.Is(err, ErrNoAvailableCopy) {
			return nil, resultReservation, nil
		}
		log.Printf("[ERROR] CheckoutBook: transaction failed for book %s / user %s: %v", bookID, userID, err)
		return nil, nil, err
	}
	return resultCheckout, nil, nil
}

// ─── Return ───────────────────────────────────────────────────────────────────

// ReturnCheckout implements the transactional return flow.
//
// Steps (all in one transaction):
//  1. Lock the Checkout row (FOR UPDATE).
//  2. Guard against double-return.
//  3. Calculate fine (see calculateFine).
//  4. Mark checkout as returned.
//  5. Mark BookCopy AVAILABLE.
//  6. If a reservation exists for that book, immediately convert it to a new checkout.
//  7. Return the updated Checkout.
func (s *libraryService) ReturnCheckout(checkoutID uuid.UUID) (*models.Checkout, error) {
	var updated *models.Checkout

	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Lock the checkout row to prevent concurrent double-returns.
		checkout, err := s.checkoutRepo.GetByIDForUpdate(tx, checkoutID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrCheckoutNotFound
			}
			return err
		}

		// Guard: already returned.
		if checkout.ReturnedAt != nil {
			log.Printf("[WARN] ReturnCheckout: checkout %s already returned at %s", checkoutID, checkout.ReturnedAt)
			return ErrCheckoutAlreadyReturned
		}

		now := time.Now().UTC()
		fine := calculateFine(checkout.DueDate, now)
		log.Printf("[INFO] ReturnCheckout: returning checkout %s (copy=%s, user=%s), fine=%d", checkoutID, checkout.BookCopyID, checkout.UserID, fine)

		// Mark as returned.
		if err := s.checkoutRepo.MarkReturned(tx, checkout.ID, now, fine); err != nil {
			log.Printf("[ERROR] ReturnCheckout: failed to mark checkout %s as returned: %v", checkoutID, err)
			return err
		}

		// Release copy back to AVAILABLE.
		if err := s.bookCopyRepo.UpdateStatus(tx, checkout.BookCopyID, models.BookCopyStatusAvailable); err != nil {
			log.Printf("[ERROR] ReturnCheckout: failed to mark copy %s AVAILABLE: %v", checkout.BookCopyID, err)
			return err
		}

		// BookCopy must be preloaded (done by GetByIDForUpdate) to access BookID.
		bookID := checkout.BookCopy.BookID

		// Check for waiting reservation.
		res, err := s.reservationRepo.GetNextForBook(tx, bookID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("[ERROR] ReturnCheckout: failed to query reservations for book %s: %v", bookID, err)
			return err
		}

		if err == nil && res != nil {
			log.Printf("[INFO] ReturnCheckout: reservation found (id=%s, user=%s, pos=%d), auto-converting to checkout", res.ID, res.UserID, res.QueuePosition)

			// Immediately re-assign the copy to the next reserved user.
			if err := s.bookCopyRepo.UpdateStatus(tx, checkout.BookCopyID, models.BookCopyStatusCheckedOut); err != nil {
				return err
			}
			if err := s.reservationRepo.Delete(tx, res.ID); err != nil {
				return err
			}

			now2 := time.Now().UTC()
			due2 := now2.AddDate(0, 0, LoanPeriodDays)
			newCheckout := &models.Checkout{
				BookCopyID: checkout.BookCopyID,
				UserID:     res.UserID,
				CheckoutAt: now2,
				DueDate:    due2,
				FineAmount: 0,
			}
			if err := s.checkoutRepo.Create(tx, newCheckout); err != nil {
				log.Printf("[ERROR] ReturnCheckout: failed to create auto-checkout for user %s: %v", res.UserID, err)
				return err
			}
			log.Printf("[INFO] ReturnCheckout: auto-checkout created (id=%s) for reserved user %s, due %s", newCheckout.ID, res.UserID, due2.Format("2006-01-02"))
		}

		// Reload updated checkout to reflect returned_at and fine_amount.
		reloaded, err := s.checkoutRepo.GetByIDForUpdate(tx, checkoutID)
		if err != nil {
			return err
		}
		updated = reloaded
		return nil
	})

	if err != nil {
		log.Printf("[ERROR] ReturnCheckout: transaction failed for checkout %s: %v", checkoutID, err)
		return nil, err
	}
	return updated, nil
}

// ─── Queries ──────────────────────────────────────────────────────────────────

// ListUserCheckouts returns all checkout records (active and past) for a user.
func (s *libraryService) ListUserCheckouts(userID uuid.UUID) ([]models.Checkout, error) {
	return s.checkoutRepo.ListByUser(nil, userID)
}

// ListReservationsForBook returns all current reservations for a book, ordered by queue_position.
func (s *libraryService) ListReservationsForBook(bookID uuid.UUID) ([]models.Reservation, error) {
	return s.reservationRepo.ListByBook(nil, bookID)
}

// ─── Internal Helpers ─────────────────────────────────────────────────────────

// createReservationWithRetry inserts a Reservation into the queue for the given book/user.
// If a unique-constraint violation occurs on (book_id, queue_position) — possible under
// concurrent load — the queue position is recalculated and the insert is retried once.
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
			log.Printf("[WARN] createReservationWithRetry: queue_position collision at pos %d for book %s, retrying", nextPos, bookID)
			// Another concurrent goroutine claimed our slot; recalculate and retry once.
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

// isUniqueViolation checks whether a PostgreSQL unique-constraint error occurred.
// PostgreSQL error code 23505 = unique_violation.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "23505")
}

// ─── Fine Calculation ─────────────────────────────────────────────────────────

// calculateFine computes the overdue fine for a returned book.
//
// Rules:
//   - Loan period  : LoanPeriodDays (14 days) from checkout date.
//   - Fine rate    : FinePerDay (10 currency units) per calendar day overdue.
//   - Minimum fine : FinePerDay (i.e. at least 1 day) if any overdue time exists.
//   - No fine      : If returnedAt is on or before dueDate.
//
// Calculation uses calendar-day truncation (midnight UTC) to avoid penalising
// users who return a book the same calendar day as the due date but after the
// exact checkout time.
func calculateFine(dueDate, returnedAt time.Time) int {
	// No fine if returned on time.
	if !returnedAt.After(dueDate) {
		return 0
	}

	// Truncate both timestamps to midnight UTC and compute full calendar days overdue.
	dueMidnight      := dueDate.UTC().Truncate(24 * time.Hour)
	returnedMidnight := returnedAt.UTC().Truncate(24 * time.Hour)

	daysLate := int(returnedMidnight.Sub(dueMidnight).Hours() / 24)

	// Enforce minimum 1-day fine (edge case: returned > dueDate but on the same calendar day).
	if daysLate < 1 {
		daysLate = 1
	}

	return daysLate * FinePerDay
}
