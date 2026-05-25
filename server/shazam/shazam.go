//go:build !js && !wasm
// +build !js,!wasm

package shazam

import (
	"fmt"
	"song-recognition/db"
	"song-recognition/utils"
	"sort"
	"time"
)

type Match struct {
	SongID     uint32
	SongTitle  string
	SongArtist string
	YouTubeID  string
	Timestamp  uint32
	Score      float64
}

  type RawMatch struct {
      SongID      uint32  `json:"songId"`
      Score       float64 `json:"score"`
      Confidence  float64 `json:"confidence"`  // score / total fingerprints
      TimestampMs int32   `json:"timestampMs"`
  }


  func FindRawMatches(
      sampleFingerprint map[uint32][]uint32,
      topN int,
      songIDs []uint32,   // pass nil or empty to search all
  ) ([]RawMatch, error) {
      addresses := make([]uint32, 0, len(sampleFingerprint))
      for address := range sampleFingerprint {
          addresses = append(addresses, address)
      }

      dbClient, err := db.NewDBClient()
      if err != nil {
          return nil, err
      }
      defer dbClient.Close()

      couples, err := dbClient.GetCouplesFiltered(addresses, songIDs)
      if err != nil {
          return nil, err
      }

      matches := map[uint32][][2]uint32{}
      for address, cs := range couples {
          for _, c := range cs {
              for _, sampleTime := range sampleFingerprint[address] {
                  matches[c.SongID] = append(
                      matches[c.SongID],
                      [2]uint32{sampleTime, c.AnchorTimeMs},
                  )
              }
          }
      }

      var totalPairs int
      for _, times := range sampleFingerprint {
          totalPairs += len(times)
      }
      totalFingerprints := float64(totalPairs)

      var result []RawMatch
      for songID, times := range matches {
          offsetCounts := make(map[int32]int)
          for _, timePair := range times {
              sampleTime := int32(timePair[0])
              dbTime := int32(timePair[1])
              offsetBucket := (dbTime - sampleTime) / 100
              offsetCounts[offsetBucket]++
          }

          var maxCount int
          var dominantBucket int32
          for bucket, count := range offsetCounts {
              if count > maxCount {
                  maxCount = count
                  dominantBucket = bucket
              }
          }

	  result = append(result, RawMatch{
	      SongID:      songID,
	      Score:       float64(maxCount),
	      Confidence:  float64(maxCount) / totalFingerprints,
	      TimestampMs: dominantBucket * 100,
	  })

      }

      sort.Slice(result, func(i, j int) bool {
          return result[i].Score > result[j].Score
      })

      if topN > 0 && len(result) > topN {
          result = result[:topN]
      }

      return result, nil
  }


// FindMatches analyzes the audio sample to find matching songs in the database.
func FindMatches(audioSample []float64, audioDuration float64, sampleRate int) ([]Match, time.Duration, error) {
	startTime := time.Now()

	spectrogram, err := Spectrogram(audioSample, sampleRate)
	if err != nil {
		return nil, time.Since(startTime), fmt.Errorf("failed to get spectrogram of samples: %v", err)
	}

	peaks := ExtractPeaks(spectrogram, audioDuration, sampleRate)
	// peaks := ExtractPeaksLMX(spectrogram, true)
	sampleFingerprint := Fingerprint(peaks, utils.GenerateUniqueID())

	sampleFingerprintMap := make(map[uint32][]uint32)
	for address, couples := range sampleFingerprint {
		for _, couple := range couples {
			sampleFingerprintMap[address] = append(sampleFingerprintMap[address], couple.AnchorTimeMs)
		}
	}

	matches, _, _ := FindMatchesFGP(sampleFingerprintMap, nil)

	return matches, time.Since(startTime), nil
}


// FindMatchesFGP uses the sample fingerprint to find matching songs in the database.
  func FindMatchesFGP(sampleFingerprint map[uint32][]uint32, songIDs []uint32) ([]Match, time.Duration, error) {
	startTime := time.Now()
	logger := utils.GetLogger()

	addresses := make([]uint32, 0, len(sampleFingerprint))
	for address := range sampleFingerprint {
		addresses = append(addresses, address)
	}

	db, err := db.NewDBClient()
	if err != nil {
		return nil, time.Since(startTime), err
	}
	defer db.Close()

	m, err := db.GetCouplesFiltered(addresses, songIDs)
	if err != nil {
		return nil, time.Since(startTime), err
	}

	matches := map[uint32][][2]uint32{}        // songID -> [(sampleTime, dbTime)]
	timestamps := map[uint32]uint32{}          // songID -> earliest timestamp
	targetZones := map[uint32]map[uint32]int{} // songID -> timestamp -> count

	for address, couples := range m {
		for _, couple := range couples {
			for _, sampleTime := range sampleFingerprint[address] {
				matches[couple.SongID] = append(
					matches[couple.SongID],
					[2]uint32{sampleTime, couple.AnchorTimeMs},
				)
			}

			if existingTime, ok := timestamps[couple.SongID]; !ok || couple.AnchorTimeMs < existingTime {
				timestamps[couple.SongID] = couple.AnchorTimeMs
			}

			if _, ok := targetZones[couple.SongID]; !ok {
				targetZones[couple.SongID] = make(map[uint32]int)
			}
			targetZones[couple.SongID][couple.AnchorTimeMs]++
		}
	}

	// matches = filterMatches(10, matches, targetZones)

	scores := analyzeRelativeTiming(matches)

	var matchList []Match

	for songID, points := range scores {
		song, songExists, err := db.GetSongByID(songID)
		if !songExists {
			logger.Info(fmt.Sprintf("song with ID (%v) doesn't exist", songID))
			continue
		}
		if err != nil {
			logger.Info(fmt.Sprintf("failed to get song by ID (%v): %v", songID, err))
			continue
		}

		match := Match{songID, song.Title, song.Artist, song.YouTubeID, timestamps[songID], points}
		matchList = append(matchList, match)
	}

	sort.Slice(matchList, func(i, j int) bool {
		return matchList[i].Score > matchList[j].Score
	})

	return matchList, time.Since(startTime), nil
}

// filterMatches filters out matches that don't have enough
// target zones to meet the specified threshold
func filterMatches(
	threshold int,
	matches map[uint32][][2]uint32,
	targetZones map[uint32]map[uint32]int) map[uint32][][2]uint32 {

	// Filter out non target zones.
	// When a target zone has less than `targetZoneSize` anchor times, it is not considered a target zone.
	for songID, anchorTimes := range targetZones {
		for anchorTime, count := range anchorTimes {
			if count < targetZoneSize {
				delete(targetZones[songID], anchorTime)
			}
		}
	}

	filteredMatches := map[uint32][][2]uint32{}
	for songID, zones := range targetZones {
		if len(zones) >= threshold {
			filteredMatches[songID] = matches[songID]
		}
	}

	return filteredMatches
}

// analyzeRelativeTiming calculates a score for each song based on the
// consistency of time offsets between the sample and database.
func analyzeRelativeTiming(matches map[uint32][][2]uint32) map[uint32]float64 {
	scores := make(map[uint32]float64)

	for songID, times := range matches {
		offsetCounts := make(map[int32]int)

		for _, timePair := range times {
			sampleTime := int32(timePair[0])
			dbTime := int32(timePair[1])
			offset := dbTime - sampleTime

			// Bin offsets in 100ms buckets to allow for small timing variations
			offsetBucket := offset / 100
			offsetCounts[offsetBucket]++
		}

		maxCount := 0
		for _, count := range offsetCounts {
			if count > maxCount {
				maxCount = count
			}
		}

		scores[songID] = float64(maxCount)
	}

	return scores
}
