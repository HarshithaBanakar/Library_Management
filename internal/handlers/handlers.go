package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"library/internal/services"
)

// LibraryHandler holds the service dependency.
type LibraryHandler struct {
	svc services.LibraryService
}

// RegisterRoutes wires all HTTP routes to handler methods.
func RegisterRoutes(r *gin.Engine, svc services.LibraryService) {
	h := &LibraryHandler{svc: svc}

	// Librarian endpoints
	r.POST("/books", h.createBook)
	r.POST("/books/:id/copies", h.addBookCopy)

	// Student endpoints
	r.POST("/books/:id/checkout", h.checkoutBook)
	r.POST("/checkouts/:id/return", h.returnCheckout)
	r.GET("/users/:id/checkouts", h.listUserCheckouts)

	// General endpoints
	r.GET("/books", h.listBooks)
	r.GET("/books/:id/reservations", h.listReservationsForBook)
}

// ─── Error Response Helper ──────────────────────────────────────────────────

// errorCode represents a machine-readable error code for clients.
type errorCode string

const (
	codeValidation      errorCode = "VALIDATION_ERROR"
	codeNotFound        errorCode = "NOT_FOUND"
	codeBusinessRule    errorCode = "BUSINESS_RULE_VIOLATION"
	codeInternalError   errorCode = "INTERNAL_ERROR"
)

// apiError writes a standardised JSON error response:
//
//	{ "error": "<message>", "code": "<ERROR_CODE>" }
func apiError(c *gin.Context, status int, message string, code errorCode) {
	c.JSON(status, gin.H{
		"error": message,
		"code":  string(code),
	})
}

// mapServiceError translates known service/domain errors to HTTP responses.
func mapServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		apiError(c, http.StatusNotFound, "resource not found", codeNotFound)
	case errors.Is(err, services.ErrBookNotFound):
		apiError(c, http.StatusNotFound, "book not found", codeNotFound)
	case errors.Is(err, services.ErrUserNotFound):
		apiError(c, http.StatusNotFound, "user not found", codeNotFound)
	case errors.Is(err, services.ErrCheckoutNotFound):
		apiError(c, http.StatusNotFound, "checkout not found", codeNotFound)
	case errors.Is(err, services.ErrCheckoutAlreadyReturned):
		apiError(c, http.StatusConflict, "checkout has already been returned", codeBusinessRule)
	case errors.Is(err, services.ErrDuplicateReservation):
		apiError(c, http.StatusConflict, "user already has an active reservation for this book", codeBusinessRule)
	case errors.Is(err, services.ErrAlreadyCheckedOut):
		apiError(c, http.StatusConflict, "this book copy is already checked out by this user", codeBusinessRule)
	default:
		apiError(c, http.StatusInternalServerError, "an internal error occurred", codeInternalError)
	}
}

// ─── Request Structs ─────────────────────────────────────────────────────────

type createBookRequest struct {
	Title       string `json:"title" binding:"required"`
	Author      string `json:"author" binding:"required"`
	TotalCopies int    `json:"total_copies" binding:"required,min=0"`
}

type checkoutRequest struct {
	UserID string `json:"user_id" binding:"required,uuid"`
}

// ─── Handlers ────────────────────────────────────────────────────────────────

func (h *LibraryHandler) createBook(c *gin.Context) {
	var req createBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, err.Error(), codeValidation)
		return
	}

	book, err := h.svc.CreateBook(req.Title, req.Author, req.TotalCopies)
	if err != nil {
		mapServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, book)
}

func (h *LibraryHandler) addBookCopy(c *gin.Context) {
	bookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid book id: must be a UUID", codeValidation)
		return
	}

	copy, err := h.svc.AddBookCopy(bookID)
	if err != nil {
		mapServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, copy)
}

func (h *LibraryHandler) checkoutBook(c *gin.Context) {
	bookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid book id: must be a UUID", codeValidation)
		return
	}

	var req checkoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, err.Error(), codeValidation)
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid user_id: must be a UUID", codeValidation)
		return
	}

	checkout, reservation, err := h.svc.CheckoutBook(bookID, userID)
	if err != nil {
		mapServiceError(c, err)
		return
	}

	if checkout != nil {
		c.JSON(http.StatusCreated, gin.H{
			"type":     "checkout",
			"checkout": checkout,
		})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"type":        "reservation",
		"reservation": reservation,
	})
}

func (h *LibraryHandler) returnCheckout(c *gin.Context) {
	checkoutID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid checkout id: must be a UUID", codeValidation)
		return
	}

	updated, err := h.svc.ReturnCheckout(checkoutID)
	if err != nil {
		mapServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *LibraryHandler) listUserCheckouts(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid user id: must be a UUID", codeValidation)
		return
	}

	checkouts, err := h.svc.ListUserCheckouts(userID)
	if err != nil {
		mapServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, checkouts)
}

func (h *LibraryHandler) listBooks(c *gin.Context) {
	books, err := h.svc.ListBooks()
	if err != nil {
		mapServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, books)
}

func (h *LibraryHandler) listReservationsForBook(c *gin.Context) {
	bookID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid book id: must be a UUID", codeValidation)
		return
	}

	reservations, err := h.svc.ListReservationsForBook(bookID)
	if err != nil {
		mapServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, reservations)
}
