# tribalwarshelp.com cron

Features:

- Adds automatically new TribalWars servers.
- Fetches TribalWars servers data (players, tribes, ODA, ODD, ODS, OD, conquers, configs).
- Saves daily player/tribe stats, player/tribe history, tribe changes, player name changes, server stats.
- Cleans the database from old player/tribe stats, player/tribe history.

## Development

**Required env variables to run this cron:**

```
DB_USER=your_db_user
DB_NAME=your_db_name
DB_PORT=5432
DB_HOST=your_db_host
DB_PASSWORD=your_db_pass

MAX_CONCURRENT_WORKERS=1 #how many servers should update at the same time
```

### Prerequisites

1. Golang
2. PostgreSQL database

### Installing

1. Clone this repo.
2. Navigate to the directory where you have cloned this repo.
3. Set the required env variables directly in your system or create .env.development file.
4. go run main.go
