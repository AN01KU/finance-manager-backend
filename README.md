# Finance Manager Backend

A Go-based expense splitting and settlement application for managing group finances using Gin framework and PostgreSQL.

## Features

- **Authentication**: JWT-based signup and login with rate limiting
- **Groups**: Create groups and manage members (creator auto-added)
- **Expenses**: Track expenses with split calculations and pagination
- **Balances**: Auto-derived balances from transactions
- **Settlements**: Record payment settlements between users
- **Personal Finance - Budgeting**: Set monthly budgets and track spending limits
- **Personal Finance - Categories**: Organize expenses with custom categories (name, color, icon)
- **Personal Finance - Expense Tracking**: Record personal expenses with date/time, descriptions, and notes
- **Personal Finance - Dashboard**: Monthly overview with spending analytics, daily averages, and projections
- **Security**: CORS protection, rate limiting, and secure JWT configuration
- **Observability**: Request logging and health checks
- **Graceful Shutdown**: Proper signal handling for clean shutdowns

## Prerequisites

- Go 1.24.2+
- Docker and Docker Compose

## Setup

### Using Docker (Recommended)

1. Clone the repository
2. Run `docker-compose up --build`
3. The application will be available at `http://localhost:8080`

The database migrations run automatically on startup.

### Local Development

If you prefer running PostgreSQL locally:

**macOS** (using Homebrew):
```bash
brew install postgresql@15
brew services start postgresql@15
createdb finance_manager
```

**Linux** (Ubuntu/Debian):
```bash
sudo apt-get install postgresql postgresql-contrib
sudo systemctl start postgresql
sudo -u postgres createdb finance_manager
```

**IMPORTANT**: Set environment variables:
```bash
export DATABASE_URL="postgres://postgres@localhost/finance_manager?sslmode=disable"
export JWT_SECRET="your-secret-must-be-at-least-32-characters-long-for-security"
export PORT="8080"
```

**Note**: The JWT_SECRET must be at least 32 characters long. The application will fail to start if this requirement is not met.

Run the application:
```bash
go run ./cmd/main.go
```

## Health Check

Check application and database health:
```bash
GET /health

Response:
{
  "status": "healthy",
  "database": "connected"
}
```

## API Endpoints

### Authentication

#### Signup
```bash
POST /auth/signup
Content-Type: application/json

{
  "email": "user@example.com",
  "username": "johndoe",
  "password": "securepassword"
}

Response:
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "email": "user@example.com",
    "username": "johndoe",
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

Response: Same as signup (includes username in user object)
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

#### Get User Groups
```bash
GET /groups
Authorization: Bearer <token>

Response:
[
  {
    "id": "650e8400-e29b-41d4-a716-446655440000",
    "name": "Weekend Trip",
    "created_by": "550e8400-e29b-41d4-a716-446655440000",
    "created_at": "2025-01-26T12:00:00Z"
  }
]
```

#### Get Group Details
```bash
GET /groups/:id
Authorization: Bearer <token>

Response:
{
  "group": {
    "id": "650e8400-e29b-41d4-a716-446655440000",
    "name": "Weekend Trip",
    "created_by": "550e8400-e29b-41d4-a716-446655440000",
    "created_at": "2025-01-26T12:00:00Z",
    "members": [
      {
        "user_id": "550e8400-e29b-41d4-a716-446655440000",
        "email": "user@example.com",
        "username": "johndoe"
      }
    ],
    "expenses": [
      {
        "id": "850e8400-e29b-41d4-a716-446655440000",
        "description": "Dinner",
        "total_amount": "100.00",
        "paid_by": "550e8400-e29b-41d4-a716-446655440000",
        "created_at": "2025-01-26T12:00:00Z"
      }
    ]
  },
  "is_member": true
}
```

#### Add Member
```bash
POST /groups/:id/add-member
Authorization: Bearer <token>
Content-Type: application/json

{
  "email": "member@example.com"
}

Response:
{
  "message": "member added"
}

Error (user not found):
{
  "error": "user not found with this email"
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
GET /groups/:id/expenses?limit=50&offset=0
Authorization: Bearer <token>

Query Parameters:
- limit: Number of expenses to return (default: 50, max: 100)
- offset: Number of expenses to skip for pagination (default: 0)

Response:
{
  "expenses": [
    {
      "id": "850e8400-e29b-41d4-a716-446655440000",
      "group_id": "650e8400-e29b-41d4-a716-446655440000",
      "description": "Dinner",
      "total_amount": "100.00",
      "paid_by": "550e8400-e29b-41d4-a716-446655440000",
      "created_at": "2025-01-26T12:00:00Z"
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "total": 150
  }
}
```

#### Get User Expenses
```bash
GET /expenses?limit=50&offset=0
Authorization: Bearer <token>

Query Parameters:
- limit: Number of expenses to return (default: 50, max: 100)
- offset: Number of expenses to skip for pagination (default: 0)

Response:
{
  "expenses": [
    {
      "id": "850e8400-e29b-41d4-a716-446655440000",
      "group_id": "650e8400-e29b-41d4-a716-446655440000",
      "description": "Dinner",
      "total_amount": "100.00",
      "paid_by": "550e8400-e29b-41d4-a716-446655440000",
      "created_at": "2025-01-26T12:00:00Z"
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "total": 150
  }
}
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
    "amount": "25.50"  // Positive = user is owed money
  },
  {
    "user_id": "750e8400-e29b-41d4-a716-446655440000",
    "amount": "-25.50"   // Negative = user owes money
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

## Personal Finance

### Budget Management

#### Set Monthly Budget
```bash
POST /budget
Authorization: Bearer <token>
Content-Type: application/json

{
  "amount": "3000.00",
  "month": 2,
  "year": 2026
}

Response:
{
  "id": "a50e8400-e29b-41d4-a716-446655440000",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "amount": "3000.00",
  "month": 2,
  "year": 2026,
  "created_at": "2026-02-14T12:00:00Z",
  "updated_at": "2026-02-14T12:00:00Z"
}
```

#### Get Monthly Budget
```bash
GET /budget?month=2&year=2026
Authorization: Bearer <token>

# Defaults to current month if parameters not provided
GET /budget

Response:
{
  "id": "a50e8400-e29b-41d4-a716-446655440000",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "amount": "3000.00",
  "month": 2,
  "year": 2026,
  "created_at": "2026-02-14T12:00:00Z",
  "updated_at": "2026-02-14T12:00:00Z"
}
```

#### List All Budgets
```bash
GET /budgets
Authorization: Bearer <token>

Response: Array of budget objects ordered by year and month (descending)
```

### Category Management

#### Create Category
```bash
POST /categories
Authorization: Bearer <token>
Content-Type: application/json

{
  "name": "Groceries",
  "color": "#4CAF50",
  "icon": "shopping_cart"
}

Response:
{
  "id": "b50e8400-e29b-41d4-a716-446655440000",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "Groceries",
  "color": "#4CAF50",
  "icon": "shopping_cart",
  "created_at": "2026-02-14T12:00:00Z"
}
```

#### List Categories
```bash
GET /categories
Authorization: Bearer <token>

Response: Array of category objects ordered by name
```

#### Update Category
```bash
PUT /categories/:id
Authorization: Bearer <token>
Content-Type: application/json

{
  "name": "Food & Groceries",
  "color": "#66BB6A"
}

Response: Updated category object
```

#### Delete Category
```bash
DELETE /categories/:id
Authorization: Bearer <token>

Response:
{
  "message": "category deleted successfully"
}
```

### Personal Expense Management

#### Create Personal Expense
```bash
POST /personal-expenses
Authorization: Bearer <token>
Content-Type: application/json

{
  "category": "Groceries",
  "amount": "45.50",
  "description": "Weekly grocery shopping",
  "notes": "Bought vegetables and fruits",
  "expense_date": "2026-02-14T10:30:00Z"
}

# Description and notes are optional
# Category can be null/empty for uncategorized expenses

Response:
{
  "id": "c50e8400-e29b-41d4-a716-446655440000",
  "user_id": "550e8400-e29b-41d4-a716-446655440000",
  "category": "Groceries",
  "amount": "45.50",
  "description": "Weekly grocery shopping",
  "notes": "Bought vegetables and fruits",
  "expense_date": "2026-02-14T10:30:00Z",
  "created_at": "2026-02-14T12:00:00Z",
  "updated_at": "2026-02-14T12:00:00Z"
}
```

#### List Personal Expenses
```bash
GET /personal-expenses?limit=50&offset=0&category=Groceries&start_date=2026-02-01&end_date=2026-02-28
Authorization: Bearer <token>

Query Parameters:
- limit: Number of expenses to return (default: 50, max: 100)
- offset: Number of expenses to skip for pagination
- category: Filter by category name (string)
- start_date: Filter expenses from this date (YYYY-MM-DD)
- end_date: Filter expenses up to this date (YYYY-MM-DD)

Response:
{
  "expenses": [
    {
      "id": "c50e8400-e29b-41d4-a716-446655440000",
      "user_id": "550e8400-e29b-41d4-a716-446655440000",
      "category": "Groceries",
      "amount": "45.50",
      "description": "Weekly grocery shopping",
      "notes": "Bought vegetables and fruits",
      "expense_date": "2026-02-14T10:30:00Z",
      "created_at": "2026-02-14T12:00:00Z",
      "updated_at": "2026-02-14T12:00:00Z"
    }
  ],
  "pagination": {
    "limit": 50,
    "offset": 0,
    "total": 150
  }
}
```

#### Get Single Expense
```bash
GET /personal-expenses/:id
Authorization: Bearer <token>

Response: Single expense object
```

#### Update Personal Expense
```bash
PUT /personal-expenses/:id
Authorization: Bearer <token>
Content-Type: application/json

{
  "amount": "50.00",
  "notes": "Updated amount after reviewing receipt"
}

# All fields are optional - only provide fields to update

Response: Updated expense object
```

#### Delete Personal Expense
```bash
DELETE /personal-expenses/:id
Authorization: Bearer <token>

Response:
{
  "message": "expense deleted successfully"
}
```

### Monthly Dashboard

#### Get Monthly Dashboard
```bash
GET /dashboard/monthly?month=2&year=2026
Authorization: Bearer <token>

# Defaults to current month if parameters not provided
GET /dashboard/monthly

Response:
{
  "month": 2,
  "year": 2026,
  "budget": "3000.00",
  "total_spent": "1250.75",
  "remaining_budget": "1749.25",
  "days_in_month": 28,
  "days_elapsed": 14,
  "days_remaining": 14,
  "daily_average_spent": "89.34",
  "projected_spending": "2501.52",
  "is_over_budget": false,
  "expense_count": 45,
  "category_breakdown": [
    {
      "category_id": "b50e8400-e29b-41d4-a716-446655440000",
      "category_name": "Groceries",
      "total_amount": "450.50",
      "expense_count": 12
    },
    {
      "category_id": "c60e8400-e29b-41d4-a716-446655440000",
      "category_name": "Transportation",
      "total_amount": "320.25",
      "expense_count": 18
    },
    {
      "category_id": null,
      "category_name": null,
      "total_amount": "480.00",
      "expense_count": 15
    }
  ]
}
```

**Dashboard Features:**
- Shows current month's budget and spending
- Calculates remaining budget (positive if under budget, negative if over)
- Tracks days elapsed and remaining in the month
- Computes daily average spending
- Projects total month spending based on current rate
- Breaks down spending by category
- Includes uncategorized expenses (null category)

## Database Schema

### users
- `id` (UUID): Primary key
- `email` (VARCHAR): Unique email address
- `username` (VARCHAR): Unique username
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

### expense_categories
- `id` (UUID): Primary key
- `user_id` (UUID): Foreign key
- `name` (VARCHAR): Category name
- `color` (VARCHAR): Hex color code
- `icon` (VARCHAR): Icon identifier
- `created_at` (TIMESTAMP): Creation time
- Unique constraint: (user_id, name)

### monthly_budgets
- `id` (UUID): Primary key
- `user_id` (UUID): Foreign key
- `amount` (DECIMAL): Budget amount
- `month` (INTEGER): Month (1-12)
- `year` (INTEGER): Year
- `created_at` (TIMESTAMP): Creation time
- `updated_at` (TIMESTAMP): Last update time
- Unique constraint: (user_id, month, year)

### personal_expenses
- `id` (UUID): Primary key
- `user_id` (UUID): Foreign key
- `category` (VARCHAR): Category name (nullable)
- `amount` (DECIMAL): Expense amount
- `description` (VARCHAR): Optional description
- `notes` (TEXT): Optional notes
- `expense_date` (TIMESTAMP): Date and time of expense
- `created_at` (TIMESTAMP): Creation time
- `updated_at` (TIMESTAMP): Last update time

## Testing with cURL

### 1. Signup
```bash
curl -X POST http://localhost:8080/auth/signup \
  -H "Content-Type: application/json" \
  -d '{"email":"alice@example.com","username":"alice","password":"password123"}'
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

## Testing with Postman

Import the `Finance_Manager_Backend.postman_collection.json` file into Postman. The collection includes:

- Pre-configured requests for all API endpoints
- Environment variables for `base_url` and `token`
- Automatic token saving after successful login/signup
- Example request bodies with proper JSON formatting

**Quick Start:**
1. Import the collection
2. Set `base_url` variable to your server URL (default: `http://localhost:8080`)
3. Run Signup or Login to get a token
4. Use the token for authenticated requests

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
│   ├── budget/              # Personal finance budgeting
│   ├── category/            # Expense categories
│   ├── config/              # Configuration
│   ├── dashboard/           # Monthly dashboard analytics
│   ├── db/                  # Database & migrations
│   ├── expense/             # Group expense operations
│   ├── group/               # Group operations
│   ├── helpers/             # Helper functions (DB utilities)
│   ├── middleware/          # JWT, CORS, rate limiting, logging
│   ├── personalexpense/     # Personal expense tracking
│   ├── settlement/          # Settlement operations
│   └── user/                # User models
├── pkg/
│   └── utils/               # Utility functions
└── go.mod                   # Dependencies
```

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

- Settlement suggestions (automated debt simplification)
- Recurring expenses (auto-create monthly expenses)
- Mobile app (iOS & Android)
- Real-time notifications (WebSocket support)
- Receipt image upload and OCR
- Multi-currency support
- Data export (CSV, PDF reports)
- Spending insights and trends
- Budget alerts and notifications

## License

MIT