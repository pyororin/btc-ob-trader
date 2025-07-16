module github.com/your-org/obi-scalp-bot

go 1.24.3

require (
	github.com/davecgh/go-spew v1.1.1 // indirect; testify dependency
	github.com/google/go-cmp v0.7.0
	github.com/gorilla/websocket v1.5.3
	// github.com/pashagolub/pgxmock/v3 v3.0.0-beta.2 // Temporarily removed for tidy
	github.com/pmezard/go-difflib v1.0.0 // indirect; testify dependency
	github.com/stretchr/testify v1.10.0
	go.uber.org/multierr v1.10.0 // indirect; zap dependency
	go.uber.org/zap v1.27.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/DATA-DOG/go-txdb v0.2.1
	github.com/golang-migrate/migrate/v4 v4.18.3
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.5
	github.com/lib/pq v1.10.9
	github.com/shopspring/decimal v1.4.0
)

require (
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.uber.org/atomic v1.7.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)
