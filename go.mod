module github.com/your-org/obi-scalp-bot

go 1.24.3

require (
	github.com/davecgh/go-spew v1.1.1 // indirect; testify dependency
	github.com/google/go-cmp v0.7.0
	github.com/gorilla/websocket v1.5.3
	// github.com/pashagolub/pgxmock/v3 v3.0.0-beta.2 // Temporarily removed for tidy
	github.com/pmezard/go-difflib v1.0.0 // indirect; testify dependency
	github.com/stretchr/testify v1.9.0
	go.uber.org/multierr v1.10.0 // indirect; zap dependency
	go.uber.org/zap v1.27.0
	gopkg.in/yaml.v3 v3.0.1
)

require github.com/jackc/pgx/v5 v5.7.5

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/rogpeppe/go-internal v1.11.0 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)
