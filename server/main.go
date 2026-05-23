package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"song-recognition/utils"
	"github.com/joho/godotenv"
	"github.com/mdobak/go-xerrors"
)

func main() {
	err := utils.CreateFolder("tmp")
	if err != nil {
		logger := utils.GetLogger()
		err := xerrors.New(err)
		ctx := context.Background()
		logger.ErrorContext(ctx, "Failed create tmp dir.", slog.Any("error", err))
	}

	err = utils.CreateFolder(SONGS_DIR)
	if err != nil {
		err := xerrors.New(err)
		logger := utils.GetLogger()
		ctx := context.Background()
		logMsg := fmt.Sprintf("failed to create directory %v", SONGS_DIR)
		logger.ErrorContext(ctx, logMsg, slog.Any("error", err))
	}

	if len(os.Args) < 2 {
		fmt.Println("Expected 'find', 'download', 'erase', 'save', 'export', 'serve' or 'fingerprint' subcommands")
		fmt.Println("\nUsage examples:")
		fmt.Println("  find <path_to_wav_file>")
		fmt.Println("  download <spotify_url>")
		fmt.Println("  erase [db | all]  (default: db)")
		fmt.Println("  save [-f|--force] <path_to_file_or_dir>")
		fmt.Println("  export [-o output.sql] <path_to_csv>")
		fmt.Println("  serve [-p <port>]")
		os.Exit(1)
	}
	_ = godotenv.Load()

	switch os.Args[1] {
	  case "find":
	      findCmd := flag.NewFlagSet("find", flag.ExitOnError)
	      songsCSV := findCmd.String("songs", "", "CSV file to limit search to specific song IDs")
	      findCmd.Parse(os.Args[2:])
	      if findCmd.NArg() < 1 {
		  fmt.Println("Usage: seek-tune find [-songs game.csv] <path_to_wav_file>")
		  os.Exit(1)
	      }
	      var songIDs []uint32
	      if *songsCSV != "" {
		  var err error
		  songIDs, err = parseSongIDsFromCSV(*songsCSV)
		  if err != nil {
		      fmt.Printf("Error reading songs CSV: %v\n", err)
		      os.Exit(1)
		  }
	      }
	      find(findCmd.Arg(0), songIDs)

	case "download":
		if len(os.Args) < 3 {
			fmt.Println("Usage: main.go download <spotify_url>")
			os.Exit(1)
		}
		url := os.Args[2]
		download(url)
  case "serve":
      serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
      port := serveCmd.String("p", "5000", "Port to listen on")
      serveCmd.Parse(os.Args[2:])
      serve(*port)

	case "erase":
		// Default is to clear only database (db mode)
		dbOnly := true
		all := false

		if len(os.Args) > 2 {
			subCmd := os.Args[2]
			switch subCmd {
			case "db":
				dbOnly = true
				all = false
			case "all":
				dbOnly = false
				all = true
			default:
				fmt.Println("Usage: main.go erase [db | all]")
				fmt.Println("  db  : only clear the database (default)")
				fmt.Println("  all : clear database and songs folder")
				os.Exit(1)
			}
		}

		erase(SONGS_DIR, dbOnly, all)
	case "save":
		indexCmd := flag.NewFlagSet("save", flag.ExitOnError)
		force := indexCmd.Bool("force", false, "save song with or without YouTube ID")
		indexCmd.BoolVar(force, "f", false, "save song with or without YouTube ID (shorthand)")
		indexCmd.Parse(os.Args[2:])
		if indexCmd.NArg() < 1 {
			fmt.Println("Usage: main.go save [-f|--force] <path_to_wav_file_or_dir>")
			os.Exit(1)
		}
		filePath := indexCmd.Arg(0)
		save(filePath, *force)
	  case "export":
	      exportCmd := flag.NewFlagSet("export", flag.ExitOnError)
	      outputFile  := exportCmd.String("o",       "fingerprints.sql", "Output file path (.sql, .csv, or .db/.sqlite3)")
	      browser     := exportCmd.String("browser", "",                 "Browser to pull cookies from (firefox, chrome, chromium)")
	      delay       := exportCmd.Int("delay",      3,                  "Seconds to sleep between downloads")
	      jitter      := exportCmd.Int("jitter",     2,                  "Extra random seconds added to delay (0–jitter)")
	      retries     := exportCmd.Int("retries",    3,                  "Max download retries per song")
	      exportCmd.Parse(os.Args[2:])
	      if exportCmd.NArg() < 1 {
		  fmt.Println("Usage: seek-tune export [-o output] [-browser firefox] [-delay 3] [-jitter 2] [-retries 3] <path_to_csv>")
		  os.Exit(1)
	      }
	      exportFingerprints(exportCmd.Arg(0), *outputFile, *browser, *delay, *jitter, *retries)

	      
	case "fingerprint":
		fpCmd := flag.NewFlagSet("fingerprint", flag.ExitOnError)
		songID := fpCmd.Uint("songId", 0, "Song ID to fingerprint")
		ytID := fpCmd.String("ytId", "", "YouTube video ID to download")
		browser := fpCmd.String("browser", "", "Browser to pull cookies from (firefox, chrome, chromium)")
		force := fpCmd.Bool("force", false, "Skip duration check (fingerprint even if longer than 10 min)")
		replace := fpCmd.Bool("replace", false, "Delete existing fingerprints and re-fingerprint")
		fpCmd.Parse(os.Args[2:])
		if *songID == 0 || *ytID == "" {
			fmt.Fprintln(os.Stderr, "Usage: seek-tune fingerprint -songId <id> -ytId <ytid> [-browser firefox] [-force] [-replace]")
			os.Exit(1)
		}
		if err := fingerprintSong(uint32(*songID), *ytID, *browser, *force, *replace); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

	  case "recognize":
	      recCmd := flag.NewFlagSet("recognize", flag.ExitOnError)
	      songsCSV := recCmd.String("songs", "", "CSV file to limit search to specific song IDs")
	      recCmd.Parse(os.Args[2:])
	      if recCmd.NArg() < 1 {
		  fmt.Fprintln(os.Stderr, "Usage: seek-tune recognize [-songs game.csv] <path_to_audio_file>")
		  os.Exit(1)
	      }
	      var songIDs []uint32
	      if *songsCSV != "" {
		  var err error
		  songIDs, err = parseSongIDsFromCSV(*songsCSV)
		  if err != nil {
		      fmt.Fprintf(os.Stderr, "Error reading songs CSV: %v\n", err)
		      os.Exit(1)
		  }
	      }
	      recognizeCmd(recCmd.Arg(0), songIDs)


	default:
		fmt.Println("Expected 'find', 'download', 'erase', 'save', or 'serve' subcommands")
		fmt.Println("\nUsage examples:")
		fmt.Println("  find <path_to_wav_file>")
		fmt.Println("  download <spotify_url>")
		fmt.Println("  erase [db | all]  (default: db)")
		fmt.Println("  save [-f|--force] <path_to_file_or_dir>")
		fmt.Println("  serve [-proto <http|https>] [-p <port>]")
		os.Exit(1)
	}
}
