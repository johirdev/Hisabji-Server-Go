hisabji-backend/
│
├── cmd/
│   └── api/
│       └── main.go
│
├── internal/
│   │
│   ├── config/
│   │   └── config.go
│   │
│   ├── domain/
│   │   ├── user.go
│   │   ├── expense.go
│   │   ├── income.go
│   │   ├── budget.go
│   │   ├── category.go
│   │   ├── subscription.go
│   │   ├── financial_goal.go
│   │   ├── forecast.go
│   │   └── notification.go
│   │
│   ├── module/
│   │   │
│   │   ├── auth/
│   │   │   ├── handler.go
│   │   │   ├── service.go
│   │   │   ├── repository.go
│   │   │   ├── dto.go
│   │   │   └── routes.go
│   │   │
│   │   ├── user/
│   │   │   ├── handler.go
│   │   │   ├── service.go
│   │   │   ├── repository.go
│   │   │   └── dto.go
│   │   │
│   │   ├── expense/
│   │   │   ├── handler.go
│   │   │   ├── service.go
│   │   │   ├── repository.go
│   │   │   ├── dto.go
│   │   │   └── routes.go
│   │   │
│   │   ├── income/
│   │   ├── budget/
│   │   ├── category/
│   │   ├── dashboard/
│   │   ├── analytics/
│   │   ├── forecast/
│   │   ├── ai/
│   │   ├── subscription/
│   │   ├── goal/
│   │   └── notification/
│   │
│   ├── infrastructure/
│   │   ├── database/
│   │   │   ├── postgres.go
│   │   │   └── redis.go
│   │   ├── cache/
│   │   ├── ai/
│   │   ├── payment/
│   │   └── queue/
│   │
│   ├── middleware/
│   │   ├── auth.go
│   │   ├── rate_limit.go
│   │   ├── logger.go
│   │   └── recovery.go
│   │
│   └── shared/
│       ├── jwt/
│       ├── hash/
│       ├── response/
│       ├── validator/
│       └── errors/
│
├── migrations/
│
├── jobs/
│   ├── daily_analysis.go
│   ├── monthly_report.go
│   └── forecast.go
│
├── docs/
│
├── tests/
│
├── .env.example
├── docker-compose.yml
├── Dockerfile
├── Makefile
├── go.mod
└── README.md