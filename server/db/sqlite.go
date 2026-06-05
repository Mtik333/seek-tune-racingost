package db

import (
	"database/sql"
	"fmt"
	"song-recognition/models"
	"song-recognition/utils"
	"strings"

	"github.com/mattn/go-sqlite3"
)

type SQLiteClient struct {
	db              *sql.DB
	commonAddresses map[uint32]struct{}
}

func NewSQLiteClient(dataSourceName string) (*SQLiteClient, error) {
	// Add busy timeout param to DSN (milliseconds)
	if !strings.Contains(dataSourceName, "_busy_timeout") {
		if strings.Contains(dataSourceName, "?") {
			dataSourceName += "&_busy_timeout=5000" // 5 seconds
		} else {
			dataSourceName += "?_busy_timeout=5000"
		}
	}

	db, err := sql.Open("sqlite3", dataSourceName)
	if err != nil {
		return nil, fmt.Errorf("error connecting to SQLite: %s", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA cache_size=-262144", // 256 MB (safe on 4 GB shared with JVM)
		"PRAGMA mmap_size=1073741824", // 1 GB
		"PRAGMA synchronous=NORMAL",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("error setting pragma (%s): %s", p, err)
		}
	}

	err = createTables(db)
	if err != nil {
		return nil, fmt.Errorf("error creating tables: %s", err)
	}

	client := &SQLiteClient{db: db}
	if err := client.loadCommonAddresses(); err != nil {
		return nil, fmt.Errorf("error loading common addresses: %s", err)
	}
	return client, nil
}


// createTables creates the required tables if they don't exist
func createTables(db *sql.DB) error {
	createSongsTable := `
    CREATE TABLE IF NOT EXISTS songs (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        title TEXT NOT NULL,
        artist TEXT NOT NULL,
        ytID TEXT,
        key TEXT NOT NULL UNIQUE
    );
    `

	createFingerprintsTable := `
    CREATE TABLE IF NOT EXISTS fingerprints (
        address INTEGER NOT NULL,
        anchorTimeMs INTEGER NOT NULL,
        songID INTEGER NOT NULL,
        PRIMARY KEY (address, anchorTimeMs, songID)
    );
    `

	createBlacklistTable := `
    CREATE TABLE IF NOT EXISTS blacklist (
        songID INTEGER PRIMARY KEY
    );
    `

	createCommonAddressesTable := `
    CREATE TABLE IF NOT EXISTS common_addresses (
        address INTEGER PRIMARY KEY
    );
    `

	_, err := db.Exec(createSongsTable)
	if err != nil {
		return fmt.Errorf("error creating songs table: %s", err)
	}

	_, err = db.Exec(createFingerprintsTable)
	if err != nil {
		return fmt.Errorf("error creating fingerprints table: %s", err)
	}

	_, err = db.Exec(createBlacklistTable)
	if err != nil {
		return fmt.Errorf("error creating blacklist table: %s", err)
	}

	_, err = db.Exec(createCommonAddressesTable)
	if err != nil {
		return fmt.Errorf("error creating common_addresses table: %s", err)
	}

	return nil
}

func (db *SQLiteClient) Close() error {
	if db.db != nil {
		return db.db.Close()
	}
	return nil
}

func (db *SQLiteClient) StoreFingerprints(fingerprints map[uint32][]models.Couple) error {
	tx, err := db.db.Begin()
	if err != nil {
		return fmt.Errorf("error starting transaction: %s", err)
	}

	stmt, err := tx.Prepare("INSERT OR REPLACE INTO fingerprints (address, anchorTimeMs, songID) VALUES (?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("error preparing statement: %s", err)
	}
	defer stmt.Close()

	for address, couples := range fingerprints {
		for _, couple := range couples {
			if _, err := stmt.Exec(address, couple.AnchorTimeMs, couple.SongID); err != nil {
				tx.Rollback()
				return fmt.Errorf("error executing statement: %s", err)
			}
		}
	}

	return tx.Commit()
}

  func (db *SQLiteClient) HasFingerprints(songID uint32) (bool, error) {
        var count int
        err := db.db.QueryRow("SELECT COUNT(*) FROM fingerprints WHERE songID = ? LIMIT 1", songID).Scan(&count)
        if err != nil {
                return false, fmt.Errorf("error checking fingerprints for songID %d: %s", songID, err)
        }
        return count > 0, nil
  }

func (db *SQLiteClient) loadCommonAddresses() error {
	rows, err := db.db.Query("SELECT address FROM common_addresses")
	if err != nil {
		return fmt.Errorf("error loading common_addresses: %s", err)
	}
	defer rows.Close()
	m := make(map[uint32]struct{})
	for rows.Next() {
		var addr uint32
		if err := rows.Scan(&addr); err != nil {
			return fmt.Errorf("error scanning common address: %s", err)
		}
		m[addr] = struct{}{}
	}
	db.commonAddresses = m
	return rows.Err()
}

func (db *SQLiteClient) RebuildCommonAddresses() error {
	fmt.Println("Rebuilding common_addresses table...")
	_, err := db.db.Exec(`
		DELETE FROM common_addresses;
		INSERT INTO common_addresses (address)
		SELECT address FROM fingerprints
		GROUP BY address
		HAVING COUNT(*) > ?`, maxSongMatchesPerAddress*5)
	if err != nil {
		return fmt.Errorf("error rebuilding common_addresses: %s", err)
	}
	if err := db.loadCommonAddresses(); err != nil {
		return err
	}
	fmt.Printf("common_addresses rebuilt: %d entries\n", len(db.commonAddresses))
	return nil
}

// maxSongMatchesPerAddress: addresses matching more songs than this are discarded as noise.
// They add no discriminative power and account for the vast majority of DB rows fetched.
const maxSongMatchesPerAddress = 500
const addressChunkSize = 900

// filterDiscriminativeAddresses removes addresses that are in the in-memory common_addresses
// set. If the set is empty (table not yet built), all addresses pass through.
func (db *SQLiteClient) filterDiscriminativeAddresses(addresses []uint32) ([]uint32, error) {
	if len(db.commonAddresses) == 0 {
		return addresses, nil
	}
	kept := make([]uint32, 0, len(addresses))
	for _, addr := range addresses {
		if _, isCommon := db.commonAddresses[addr]; !isCommon {
			kept = append(kept, addr)
		}
	}
	return kept, nil
}

func (db *SQLiteClient) queryCouplesInChunks(addresses []uint32, filterSet map[uint32]bool) (map[uint32][]models.Couple, error) {
	discriminative, err := db.filterDiscriminativeAddresses(addresses)
	if err != nil {
		return nil, err
	}
	couples := make(map[uint32][]models.Couple)
	for i := 0; i < len(discriminative); i += addressChunkSize {
		end := i + addressChunkSize
		if end > len(discriminative) {
			end = len(discriminative)
		}
		chunk := discriminative[i:end]

		placeholders := make([]string, len(chunk))
		args := make([]interface{}, len(chunk))
		for j, addr := range chunk {
			placeholders[j] = "?"
			args[j] = addr
		}
		query := fmt.Sprintf("SELECT address, anchorTimeMs, songID FROM fingerprints WHERE address IN (%s)",
			strings.Join(placeholders, ","))

		rows, err := db.db.Query(query, args...)
		if err != nil {
			return nil, fmt.Errorf("error querying database: %s", err)
		}
		for rows.Next() {
			var address uint32
			var couple models.Couple
			if err := rows.Scan(&address, &couple.AnchorTimeMs, &couple.SongID); err != nil {
				rows.Close()
				return nil, fmt.Errorf("error scanning row: %s", err)
			}
			if len(filterSet) == 0 || filterSet[couple.SongID] {
				couples[address] = append(couples[address], couple)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("error iterating rows: %s", err)
		}
	}
	return couples, nil
}

func (db *SQLiteClient) GetCouples(addresses []uint32) (map[uint32][]models.Couple, error) {
	return db.queryCouplesInChunks(addresses, nil)
}

func (db *SQLiteClient) GetCouplesFiltered(addresses []uint32, songIDs []uint32) (map[uint32][]models.Couple, error) {
	filterSet := make(map[uint32]bool, len(songIDs))
	for _, id := range songIDs {
		filterSet[id] = true
	}
	return db.queryCouplesInChunks(addresses, filterSet)
}


func (db *SQLiteClient) TotalSongs() (int, error) {
	var count int
	err := db.db.QueryRow("SELECT COUNT(*) FROM songs").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("error counting songs: %s", err)
	}
	return count, nil
}

func (db *SQLiteClient) RegisterSong(songTitle, songArtist, ytID string) (uint32, error) {
	tx, err := db.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("error starting transaction: %s", err)
	}

	stmt, err := tx.Prepare("INSERT INTO songs (id, title, artist, ytID, key) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("error preparing statement: %s", err)
	}
	defer stmt.Close()

	songID := utils.GenerateUniqueID()
	songKey := utils.GenerateSongKey(songTitle, songArtist)
	if _, err := stmt.Exec(songID, songTitle, songArtist, ytID, songKey); err != nil {
		tx.Rollback()
		if sqliteErr, ok := err.(sqlite3.Error); ok && sqliteErr.Code == sqlite3.ErrConstraint {
			return 0, fmt.Errorf("song with ytID or key already exists: %v", err)
		}
		return 0, fmt.Errorf("failed to register song: %v", err)
	}

	return songID, tx.Commit()
}

var sqlitefilterKeys = "id | ytID | key"

// GetSong retrieves a song by filter key
func (s *SQLiteClient) GetSong(filterKey string, value interface{}) (Song, bool, error) {

	if !strings.Contains(sqlitefilterKeys, filterKey) {
		return Song{}, false, fmt.Errorf("invalid filter key")
	}

	query := fmt.Sprintf("SELECT title, artist, ytID FROM songs WHERE %s = ?", filterKey)

	row := s.db.QueryRow(query, value)

	var song Song
	err := row.Scan(&song.Title, &song.Artist, &song.YouTubeID)
	if err != nil {
		if err == sql.ErrNoRows {
			return Song{}, false, nil
		}
		return Song{}, false, fmt.Errorf("failed to retrieve song: %s", err)
	}

	return song, true, nil
}

func (db *SQLiteClient) GetSongByID(songID uint32) (Song, bool, error) {
	return db.GetSong("id", songID)
}

func (db *SQLiteClient) GetSongByYTID(ytID string) (Song, bool, error) {
	return db.GetSong("ytID", ytID)
}

func (db *SQLiteClient) GetSongByKey(key string) (Song, bool, error) {
	return db.GetSong("key", key)
}

// DeleteSongByID deletes a song by ID
func (db *SQLiteClient) DeleteSongByID(songID uint32) error {
	_, err := db.db.Exec("DELETE FROM songs WHERE id = ?", songID)
	if err != nil {
		return fmt.Errorf("failed to delete song: %v", err)
	}
	return nil
}

// DeleteCollection deletes a collection (table) from the database
func (db *SQLiteClient) DeleteCollection(collectionName string) error {
	_, err := db.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", collectionName))
	if err != nil {
		return fmt.Errorf("error deleting collection: %v", err)
	}
	return nil
}

func (db *SQLiteClient) DeleteFingerprintsBySongID(songID uint32) error {
	_, err := db.db.Exec("DELETE FROM fingerprints WHERE songID = ?", songID)
	if err != nil {
		return fmt.Errorf("error deleting fingerprints for song %d: %s", songID, err)
	}
	return nil
}

// FillBlacklistByDuration inserts into blacklist all songIDs whose MAX(anchorTimeMs)
// across fingerprints exceeds thresholdMs. Returns the number of newly inserted rows.
func (db *SQLiteClient) FillBlacklistByDuration(thresholdMs int) (int, error) {
	res, err := db.db.Exec(`
		INSERT OR IGNORE INTO blacklist (songID)
		SELECT songID
		FROM fingerprints
		GROUP BY songID
		HAVING MAX(anchorTimeMs) > ?
	`, thresholdMs)
	if err != nil {
		return 0, fmt.Errorf("error filling blacklist: %s", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (db *SQLiteClient) AddToBlacklist(songID uint32) error {
	_, err := db.db.Exec("INSERT OR IGNORE INTO blacklist (songID) VALUES (?)", songID)
	if err != nil {
		return fmt.Errorf("error adding to blacklist: %s", err)
	}
	return nil
}

func (db *SQLiteClient) IsBlacklisted(songID uint32) (bool, error) {
	var count int
	err := db.db.QueryRow("SELECT COUNT(*) FROM blacklist WHERE songID = ?", songID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("error checking blacklist: %s", err)
	}
	return count > 0, nil
}
