# Finance Manager Backend

A Go-based expense splitting and settlement application for managing group finances using Gin framework and PostgreSQL.

## Features

- **Authentication**: JWT-based signup and login
- **Groups**: Create groups and manage members
- **Expenses**: Track expenses with split calculations
- **Balances**: Auto-derived balances from transactions
- **Settlements**: Record payment settlements between users
- **ACID Compliance**: Transactional expense operations

## Prerequisites

- Go 1.24.2+
- PostgreSQL 12+
- macOS/Linux/Windows

## Setup

### 1. Install PostgreSQL

**macOS** (using Homebrew):
```bash
brew install postgresql@15
brew services start postgresql@15
```

**Linux** (Ubuntu/Debian):
```bash
sudo apt-get install postgresql postgresql-contrib
sudo systemctl start postgresql
```

### 2. Create Database

```bash
createdb finance_manager

# Or connect to PostgreSQL and run:
# CREATE DATABASE finance_manager;
```

### 3. Configure Environment

Create a `.env` file (or set environment variables):

```bash
export DATABASE_URL="postgres://localhost/finance_manager?sslmode=disable"
export JWT_SECRET="your-secure-secret-key-change-this"
export PORT="8080"
```

**Default values** (if not set):
- `DATABASE_URL`: `postgres://user:password@localhost:5432/finance_manager?sslmode=disable`
- `JWT_SECRET`: `your-secret-key`
- `PORT`: `8080`

### 4. Run the Application

```bash
./finance-manager
```

The server will:
1. Connect to PostgreSQL
2. Run database migrations automatically
3. Start on configured port (default: 8080)

**Output:**
```
Server starting on port 8080
```

## API Endpoints

### Authentication

#### Signup
```bash
POST /auth/signup
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "securepassword"
}

Response:
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "created_at": "2025-01-26T12:00:00Z"
  }
}
```

#### Login
```bash
POST /auth/login
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "securepassword"
}

Response: Same as signup
```

### Groups

#### Create Group
```bash
POST /groups
Authorization: Bearer <token>
Content-Type: application/json

{
  "name": "Weekend Trip"
}

Response:
{
  "id": "650e8400-e29b-41d4-a716-446655440000",
  "name": "Weekend Trip",
  "created_by": "550e8400-e29b-41d4-a716-446655440000",
  "created_at": "2025-01-26T12:00:00Z"
}
```

#### Add Member
```bash
POST /groups/:id/add-member
Authorization: Bearer <token>
Content-Type: application/json

{
  "user_id": "750e8400-e29b-41d4-a716-446655440000"
}

Response:
{
  "message": "member added"
}
```

### Expenses

#### Create Expense
```bash
POST /expenses
Authorization: Bearer <token>
Content-Type: application/json

{
  "group_id": "650e8400-e29b-41d4-a716-446655440000",
  "description": "Dinner",
  "total_amount": "100.00",
  "splits": [
    {
      "user_id": "550e8400-e29b-41d4-a716-446655440000",
      "amount": "50.00"
    },
    {
      "user_id": "750e8400-e29b-41d4-a716-446655440000",
      "amount": "50.00"
    }
  ]
}

Response:
{
  "id": "850e8400-e29b-41d4-a716-446655440000",
  "group_id": "650e8400-e29b-41d4-a716-446655440000",
  "description": "Dinner",
  "total_amount": "100.00",
  "paid_by": "550e8400-e29b-41d4-a716-446655440000",
  "created_at": "2025-01-26T12:00:00Z",
  "splits": [...]
}
```

#### Get Group Expenses
```bash
GET /groups/:id/expenses
Authorization: Bearer <token>

Response: Array of expenses
```

### Balances

#### Get Group Balances
```bash
GET /groups/:id/balances
Authorization: Bearer <token>

Response:
[
  {
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "amount": "-25.50"  # Negative = is owed, Positive = owes
  },
  {
    "user_id": "750e8400-e29b-41d4-a716-446655440000",
    "amount": "25.50"   # Owes 25.50
  }
]
```

### Settlements

#### Create Settlement
```bash
POST /settlements
Authorization: Bearer <token>
Content-Type: application/json

{
  "group_id": "650e8400-e29b-41d4-a716-446655440000",
  "from_user": "750e8400-e29b-41d4-a716-446655440000",
  "to_user": "550e8400-e29b-41d4-a716-446655440000",
  "amount": "25.50"
}

Response:
{
  "id": "950e8400-e29b-41d4-a716-446655440000",
  "group_id": "650e8400-e29b-41d4-a716-446655440000",
  "from_user": "750e8400-e29b-41d4-a716-446655440000",
  "to_user": "550e8400-e29b-41d4-a716-446655440000",
  "amount": "25.50",
  "created_at": "2025-01-26T12:00:00Z"
}
```

## Database Schema

### users
- `id` (UUID): Primary key
- `email` (VARCHAR): Unique email address
- `password_hash` (VARCHAR): Bcrypt hash
- `created_at` (TIMESTAMP): Creation time

### groups
- `id` (UUID): Primary key
- `name` (VARCHAR): Group name
- `created_by` (UUID): Creator user ID
- `created_at` (TIMESTAMP): Creation time

### group_members
- `group_id` (UUID): Foreign key
- `user_id` (UUID): Foreign key
- `joined_at` (TIMESTAMP): Join time
- Primary key: (group_id, user_id)

### expenses
- `id` (UUID): Primary key
- `group_id` (UUID): Foreign key
- `description` (TEXT): Expense description
- `total_amount` (DECIMAL): Total amount
- `paid_by` (UUID): User who paid
- `created_at` (TIMESTAMP): Creation time

### expense_splits
- `expense_id` (UUID): Foreign key
- `user_id` (UUID): Foreign key
- `amount` (DECIMAL): Split amount
- Primary key: (expense_id, user_id)

### settlements
- `id` (UUID): Primary key
- `group_id` (UUID): Foreign key
- `from_user` (UUID): Payer
- `to_user` (UUID): Payee
- `amount` (DECIMAL): Settlement amount
- `created_at` (TIMESTAMP): Creation time

## Testing with cURL

### 1. Signup
```bash
curl -X POST http://localhost:8080/auth/signup \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@example.com","password":"password123"}'
```

### 2. Login
```bash
curl -X POST http://localhost:8080/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@example.com","password":"password123"}'
```

### 3. Create Group (replace TOKEN)
```bash
TOKEN="eyJhbGciOiJIUzI1NiIs..."
curl -X POST http://localhost:8080/groups \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Trip"}'
```

## Development

### Build
```bash
go build -o finance-manager ./cmd/main.go
```

### Run with air (hot reload)
```bash
go install github.com/cosmtrek/air@latest
air
```

### Run tests
```bash
go test ./...
```

## Project Structure

```
.
├── cmd/
│   └── main.go              # Application entry point
├── internal/
│   ├── auth/                # Authentication & JWT
│   ├── config/              # Configuration
│   ├── db/                  # Database & migrations
│   ├── middleware/          # JWT middleware
│   ├── user/                # User models
│   ├── group/               # Group operations
│   ├── expense/             # Expense operations
│   └── settlement/          # Settlement operations
├── pkg/
│   └── utils/               # Utility functions
└── go.mod                   # Dependencies
```

## Key Implementation Details

### ACID Compliance
- Expenses use database transactions
- Automatic rollback on validation failure
- Ensures data consistency

### Balance Calculation
- Derived from expenses and settlements
- Not stored (prevents corruption)
- Calculated on-demand for accuracy

### Security
- Passwords hashed with bcrypt
- JWTs expire after 24 hours
- UUID primary keys (not sequential)
- Parameterized SQL queries (no injection)

### Validation
- Strict input validation
- Email format verification
- UUID validation
- Split sum validation
- Group membership checks

## Troubleshooting

### Database Connection Error
```
Failed to connect to database: connection refused
```
- Ensure PostgreSQL is running: `brew services start postgresql@15`
- Check DATABASE_URL is correct
- Verify database exists: `createdb finance_manager`

### Migration Error
```
failed to run migrations: permission denied
```
- Ensure migrations folder exists: `internal/db/migrations/`
- Check file permissions: `chmod 644 internal/db/migrations/*.sql`

### Invalid Token Error
```
invalid token
```
- Ensure token is passed in Authorization header: `Bearer <token>`
- Check JWT_SECRET matches between signup and verification
- Verify token hasn't expired (24 hours)

## Performance Optimizations

- Connection pooling via pgx
- Indexed foreign keys for fast joins
- Decimal arithmetic for precise calculations
- Context-aware database operations

## Future Enhancements

- Settlement suggestions
- Expense categories
- Recurring expenses
- Mobile app
- Real-time notifications
- Audit logging

## License

MIT