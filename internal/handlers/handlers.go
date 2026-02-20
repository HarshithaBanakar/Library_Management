package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"library/internal/services"
)

type LibraryHandler struct {
	svc services.LibraryService
}

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

type createBookRequest struct {
	Title       string `json:"title" binding:"required"`
	Author      string `json:"author" binding:"required"`
	TotalCopies int    `json:"total_copies" binding:"required,min=0"`
}

func (h *LibraryHandler) createBook(c *gin.Context) {
	var req createBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	book, err := h.svc.CreateBook(req.Title, req.Author, req.TotalCopies)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, book)
}

type addBookCopyRequest struct {
	// no body currently required, kept for future extension
}

func (h *LibraryHandler) addBookCopy(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := uuid.Parse(bookIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid book id"})
		return
	}

	copy, err := h.svc.AddBookCopy(bookID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, copy)
}

type checkoutRequest struct {
	UserID string `json:"user_id" binding:"required,uuid"`
}

func (h *LibraryHandler) checkoutBook(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := uuid.Parse(bookIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid book id"})
		return
	}

	var req checkoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	checkout, reservation, err := h.svc.CheckoutBook(bookID, userID)
	if err != nil {
		// domain-level errors should be surfaced as 409/400 etc., but current service returns only generic errors
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
	checkoutIDStr := c.Param("id")
	checkoutID, err := uuid.Parse(checkoutIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid checkout id"})
		return
	}

	updated, err := h.svc.ReturnCheckout(checkoutID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (h *LibraryHandler) listUserCheckouts(c *gin.Context) {
	userIDStr := c.Param("id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	checkouts, err := h.svc.ListUserCheckouts(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, checkouts)
}

func (h *LibraryHandler) listBooks(c *gin.Context) {
	books, err := h.svc.ListBooks()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, books)
}

func (h *LibraryHandler) listReservationsForBook(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := uuid.Parse(bookIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid book id"})
		return
	}

	reservations, err := h.svc.ListReservationsForBook(bookID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, reservations)
}

