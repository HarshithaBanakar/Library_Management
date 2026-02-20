## AI Prompts Used

This project was generated with the help of an AI coding assistant. The key user prompt that guided the implementation is summarized below.

### Primary Prompt

> Generate a complete production-style REST API project in Go for a Library Book Management System using:
> - Go (latest stable), Gin for routing, PostgreSQL, GORM, and UUID primary keys.
> - Clean architecture layout with `cmd/`, `internal/handlers`, `internal/services`, `internal/repositories`, `internal/models`, `configs/`, and `migrations/`.
> - Support for user roles (STUDENT, LIBRARIAN), entities (Users, Books, BookCopies, Checkouts, Reservations) with foreign-key constraints.
> - Transactional and concurrency-safe checkout/return workflows with row locking:
>   - Checkout: lock AVAILABLE `BookCopy`, or enqueue a FIFO reservation if none exist.
>   - Return: mark checkout returned, free the copy, and assign it to the next reservation when present.
> - Fine calculation based on days late (`fine = days_late * 10`) implemented in the service layer.
> - Required endpoints:
>   - Librarian: `POST /books`, `POST /books/{id}/copies`
>   - Student: `POST /books/{id}/checkout`, `POST /checkouts/{id}/return`, `GET /users/{id}/checkouts`
>   - General: `GET /books`, `GET /books/{id}/reservations`
> - No frontend or authentication, but with professional error handling, migrations, README, DESIGN (with architecture/ER/sequence diagrams), and AI_PROMPTS documentation.

The assistant then iteratively:

- Created the Go module and project structure.
- Implemented models, repositories, and migrations aligned with the schema.
- Implemented the service layer with transactional checkout/return logic, reservation queue handling, and fine calculation.
- Implemented Gin handlers and routing for all specified endpoints.
- Authored `README.md` and `DESIGN.md` with setup instructions, API docs, transaction and concurrency explanations, and diagrams.

