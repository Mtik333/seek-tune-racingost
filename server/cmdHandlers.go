package main

import (
	"context"
	"fmt"
	"encoding/json"
	"log"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"song-recognition/db"
	"song-recognition/shazam"
	"song-recognition/spotify"
	"song-recognition/utils"
	"song-recognition/wav"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/mdobak/go-xerrors"
)

const (
	SONGS_DIR = "songs"
)

var yellow = color.New(color.FgYellow)

  func find(filePath string, songIDs []uint32) {
	wavFilePath, err := wav.ConvertToWAV(filePath)
	if err != nil {
		yellow.Println("Error converting to WAV:", err)
		return
	}

	fingerprint, err := shazam.FingerprintAudio(wavFilePath, utils.GenerateUniqueID())
	if err != nil {
		yellow.Println("Error generating fingerprint for sample: ", err)
		return
	}

	sampleFingerprint := make(map[uint32][]uint32)
	for address, couples := range fingerprint {
		for _, couple := range couples {
			sampleFingerprint[address] = append(sampleFingerprint[address], couple.AnchorTimeMs)
		}
	}

	matches, searchDuration, err := shazam.FindMatchesFGP(sampleFingerprint, songIDs)
	if err != nil {
		yellow.Println("Error finding matches:", err)
		return
	}

	if len(matches) == 0 {
		fmt.Println("\nNo match found.")
		fmt.Printf("\nSearch took: %s\n", searchDuration)
		return
	}

	msg := "Matches:"
	topMatches := matches
	if len(matches) >= 20 {
		msg = "Top 20 matches:"
		topMatches = matches[:20]
	}

	fmt.Println(msg)
	for _, match := range topMatches {
		fmt.Printf("\t- %s by %s, score: %.2f\n",
			match.SongTitle, match.SongArtist, match.Score)
	}

	fmt.Printf("\nSearch took: %s\n", searchDuration)
	topMatch := topMatches[0]
	fmt.Printf("\nFinal prediction: %s by %s , score: %.2f\n",
		topMatch.SongTitle, topMatch.SongArtist, topMatch.Score)
}

func download(spotifyURL string) {
	err := utils.CreateFolder(SONGS_DIR)
	if err != nil {
		err := xerrors.New(err)
		logger := utils.GetLogger()
		ctx := context.Background()
		logMsg := fmt.Sprintf("failed to create directory %v", SONGS_DIR)
		logger.ErrorContext(ctx, logMsg, slog.Any("error", err))
	}

	if strings.Contains(spotifyURL, "album") {
		_, err := spotify.DlAlbum(spotifyURL, SONGS_DIR)
		if err != nil {
			yellow.Println("Error: ", err)
		}
	}

	if strings.Contains(spotifyURL, "playlist") {
		_, err := spotify.DlPlaylist(spotifyURL, SONGS_DIR)
		if err != nil {
			yellow.Println("Error: ", err)
		}
	}

	if strings.Contains(spotifyURL, "track") {
		_, err := spotify.DlSingleTrack(spotifyURL, SONGS_DIR)
		if err != nil {
			yellow.Println("Error: ", err)
		}
	}
}

  func serve(port string) {
        apiKey := os.Getenv("API_KEY")
        if apiKey == "" {
                log.Fatal("API_KEY environment variable is not set")
        }

        http.HandleFunc("/recognize", func(w http.ResponseWriter, r *http.Request) {
                if r.Method != http.MethodPost {
                        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
                        return
                }

                if r.Header.Get("X-API-Key") != apiKey {
                        http.Error(w, "unauthorized", http.StatusUnauthorized)
                        return
                }
                r.ParseMultipartForm(32 << 20)
                file, header, err := r.FormFile("audio")
		log.Printf("recognize request from %s — file: %s", r.RemoteAddr, header.Filename)
                if err != nil {
                        http.Error(w, "missing audio field", http.StatusBadRequest)
                        return
                }
                defer file.Close()

                tmp, err := os.CreateTemp("tmp", "recognize-*"+filepath.Ext(header.Filename))
                if err != nil {
                        http.Error(w, "internal error", http.StatusInternalServerError)
                        return
                }
                defer os.Remove(tmp.Name())

                if _, err := io.Copy(tmp, file); err != nil {
                        tmp.Close()
                        http.Error(w, "internal error", http.StatusInternalServerError)
                        return
                }
                tmp.Close()

		  var songIDs []uint32
		  if csvPath := r.FormValue("songs"); csvPath != "" {
		      songIDs, err = parseSongIDsFromCSV(csvPath)
		      if err != nil {
			  http.Error(w, "invalid songs filter", http.StatusBadRequest)
			  return
		      }
		  } else if songIdsStr := r.FormValue("songIds"); songIdsStr != "" {
		      for _, idStr := range strings.Split(songIdsStr, ",") {
			  idStr = strings.TrimSpace(idStr)
			  if idStr == "" {
			      continue
			  }
			  id, err := strconv.ParseUint(idStr, 10, 32)
			  if err == nil {
			      songIDs = append(songIDs, uint32(id))
			  }
		      }
		  }


                fingerprint, err := shazam.FingerprintAudio(tmp.Name(), 0)
                if err != nil {
                        http.Error(w, "fingerprinting failed", http.StatusInternalServerError)
                        return
                }

                sampleFP := make(map[uint32][]uint32, len(fingerprint))
                for address, couples := range fingerprint {
                        for _, couple := range couples {
                                sampleFP[address] = append(sampleFP[address], couple.AnchorTimeMs)
                        }
                }

                matches, err := shazam.FindRawMatches(sampleFP, 5, songIDs)
                if err != nil {
                        http.Error(w, "recognition failed", http.StatusInternalServerError)
                        return
                }
		log.Printf("recognize complete — %d matches returned", len(matches))

                w.Header().Set("Content-Type", "application/json")
                json.NewEncoder(w).Encode(matches)
        })

        addr := ":" + port
        log.Printf("Starting HTTP server on %s\n", addr)
        if err := http.ListenAndServe(addr, nil); err != nil {
                log.Fatalf("HTTP server error: %v", err)
        }
  }


func erase(songsDir string, dbOnly bool, all bool) {
	logger := utils.GetLogger()
	ctx := context.Background()

	// wipe db
	dbClient, err := db.NewDBClient()
	if err != nil {
		msg := fmt.Sprintf("Error creating DB client: %v\n", err)
		logger.ErrorContext(ctx, msg, slog.Any("error", err))
	}

	err = dbClient.DeleteCollection("fingerprints")
	if err != nil {
		msg := fmt.Sprintf("Error deleting collection: %v\n", err)
		logger.ErrorContext(ctx, msg, slog.Any("error", err))
	}

	err = dbClient.DeleteCollection("songs")
	if err != nil {
		msg := fmt.Sprintf("Error deleting collection: %v\n", err)
		logger.ErrorContext(ctx, msg, slog.Any("error", err))
	}

	fmt.Println("Database cleared")

	// delete song files only if -all flag is set
	if all {
		err = filepath.Walk(songsDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() {
				ext := filepath.Ext(path)
				if ext == ".wav" || ext == ".m4a" {
					err := os.Remove(path)
					if err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			msg := fmt.Sprintf("Error walking through directory %s: %v\n", songsDir, err)
			logger.ErrorContext(ctx, msg, slog.Any("error", err))
		}
		fmt.Println("Songs folder cleared")
	}

	fmt.Println("Erase complete")
}

func save(path string, force bool) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		fmt.Printf("Error stating path %v: %v\n", path, err)
		return
	}

	if fileInfo.IsDir() {
		var filePaths []string
		err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				fmt.Printf("Error walking the path %v: %v\n", filePath, err)
				return err
			}
			// Process only files, skip directories
			if !info.IsDir() {
				filePaths = append(filePaths, filePath)
			}
			return nil
		})
		if err != nil {
			fmt.Printf("Error walking the directory %v: %v\n", path, err)
			return
		}

		processFilesConCurrently(filePaths, force)
	} else {
		err := saveSong(path, force)
		if err != nil {
			fmt.Printf("Error saving song (%v): %v\n", path, err)
		}
	}
}

func processFilesConCurrently(filePaths []string, force bool) {
	maxWorkers := runtime.NumCPU() / 2
	numFiles := len(filePaths)

	if numFiles == 0 {
		return
	}

	if numFiles < maxWorkers {
		maxWorkers = numFiles
	}

	jobs := make(chan string, numFiles)
	results := make(chan error, numFiles)

	for w := 0; w < maxWorkers; w++ {
		go func(workerID int) {
			for filePath := range jobs {
				err := saveSong(filePath, force)
				results <- err
			}
		}(w + 1)
	}

	for _, filePath := range filePaths {
		jobs <- filePath
	}
	close(jobs)

	successCount := 0
	errorCount := 0
	for i := 0; i < numFiles; i++ {
		err := <-results
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			errorCount++
		} else {
			successCount++
		}
	}

	fmt.Printf("\n ->> Processed %d files: %d successful, %d failed\n", numFiles, successCount, errorCount)
}

func saveSong(filePath string, force bool) error {
	metadata, err := wav.GetMetadata(filePath)
	if err != nil {
		return err
	}

	durationFloat, err := strconv.ParseFloat(metadata.Format.Duration, 64)
	if err != nil {
		return fmt.Errorf("failed to parse duration to float: %v", err)
	}

	tags := metadata.Format.Tags
	track := &spotify.Track{
		Album:    tags["album"],
		Artist:   tags["artist"],
		Title:    tags["title"],
		Duration: int(math.Round(durationFloat)),
	}

	ytID, err := spotify.GetYoutubeId(*track)
	if err != nil && !force {
		return fmt.Errorf("failed to get YouTube ID for song: %v", err)
	}

	fileName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	if track.Title == "" {
		// If title is empty, use the file name
		track.Title = fileName
	}

	if track.Artist == "" {
		return fmt.Errorf("no artist found in metadata")
	}

	err = spotify.ProcessAndSaveSong(filePath, track.Title, track.Artist, ytID)
	if err != nil {
		return fmt.Errorf("failed to process or save song: %v", err)
	}

	// Move song in wav format to songs directory
	wavFile := fileName + ".wav"
	sourcePath := filepath.Join(filepath.Dir(filePath), wavFile)
	newFilePath := filepath.Join(SONGS_DIR, wavFile)
	err = utils.MoveFile(sourcePath, newFilePath)
	if err != nil {
		return fmt.Errorf("failed to rename temporary file to output file: %v", err)
	}

	return nil
}

func fingerprintSong(songID uint32, ytID string, browser string, force bool, replace bool) error {
	dbClient, err := db.NewDBClient()
	if err != nil {
		return fmt.Errorf("db error: %v", err)
	}
	defer dbClient.Close()

	if replace {
		if err := dbClient.DeleteFingerprintsBySongID(songID); err != nil {
			return fmt.Errorf("failed to delete existing fingerprints: %v", err)
		}
		fmt.Printf("song %d: existing fingerprints deleted\n", songID)
	} else {
		exists, err := dbClient.HasFingerprints(songID)
		if err != nil {
			return fmt.Errorf("db check error: %v", err)
		}
		if exists {
			fmt.Printf("song %d already fingerprinted, skipping\n", songID)
			return nil
		}
	}

	fmt.Printf("song %d: downloading ytID=%s...\n", songID, ytID)
	audioPath, err := spotify.DownloadByYTID(ytID, "tmp", browser)
	if err != nil {
		return fmt.Errorf("download failed: %v", err)
	}

	if !force && !replace {
		if meta, metaErr := wav.GetMetadata(audioPath); metaErr == nil {
			if durationSec, parseErr := strconv.ParseFloat(meta.Format.Duration, 64); parseErr == nil && durationSec > 600 {
				os.Remove(audioPath)
				dbClient.AddToBlacklist(songID)
				return fmt.Errorf("duration %.0fs exceeds 10 min limit, added to blacklist", durationSec)
			}
		}
	}

	fmt.Printf("song %d: fingerprinting...\n", songID)
	fingerprints, err := shazam.FingerprintAudio(audioPath, songID)
	os.Remove(audioPath)
	if err != nil {
		return fmt.Errorf("fingerprinting failed: %v", err)
	}

	if err := dbClient.StoreFingerprints(fingerprints); err != nil {
		return fmt.Errorf("store failed: %v", err)
	}

	fmt.Printf("song %d: done (%d fingerprints)\n", songID, len(fingerprints))
	return nil
}

  func recognizeCmd(filePath string, songIDs []uint32) {
      fingerprint, err := shazam.FingerprintAudio(filePath, 0)
      if err != nil {
          fmt.Fprintf(os.Stderr, "Error fingerprinting: %v\n", err)
          os.Exit(1)
      }
      
      sampleFP := make(map[uint32][]uint32, len(fingerprint))
      for address, couples := range fingerprint {
          for _, couple := range couples {
              sampleFP[address] = append(sampleFP[address], couple.AnchorTimeMs)
          }
      }

	matches, err := shazam.FindRawMatches(sampleFP, 5, songIDs)
      if err != nil { 
          fmt.Fprintf(os.Stderr, "Error finding matches: %v\n", err)
          os.Exit(1)
      }
      
      data, err := json.Marshal(matches)
      if err != nil {
          fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
          os.Exit(1)
      }   
  
      fmt.Println(string(data))
  }   

