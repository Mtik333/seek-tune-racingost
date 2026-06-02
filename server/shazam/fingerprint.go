package shazam

import (
	"fmt"
	"song-recognition/models"
	"song-recognition/wav"
)

const (
	maxFreqBits        = 9
	maxDeltaBits       = 14
	targetZoneSize     = 3
	maxCouplesPerAddr  = 5
)

// Fingerprint generates fingerprints from a list of peaks and stores them in an array.
// Each fingerprint consists of an address and a couple.
// The address is a hash. The couple contains the anchor time and the song ID.
func Fingerprint(peaks []Peak, songID uint32) map[uint32][]models.Couple {
	fingerprints := map[uint32][]models.Couple{}

	for i, anchor := range peaks {
		for j := i + 1; j < len(peaks) && j <= i+targetZoneSize; j++ {
			target := peaks[j]

			address := createAddress(anchor, target)
			anchorTimeMs := uint32(anchor.Time * 1000)

			if len(fingerprints[address]) < maxCouplesPerAddr {
				fingerprints[address] = append(fingerprints[address], models.Couple{
					AnchorTimeMs: anchorTimeMs,
					SongID:       songID,
				})
			}
		}
	}

	return fingerprints
}

// createAddress generates a unique address for a pair of anchor and target points.
// The address is a 32-bit integer where certain bits represent the frequency of
// the anchor and target points, and other bits represent the time difference (delta time)
// between them. This function combines these components into a single address (a hash).
func createAddress(anchor, target Peak) uint32 {
	anchorFreqBin := uint32(anchor.Freq / 10) // Scale down to fit in 9 bits
	targetFreqBin := uint32(target.Freq / 10)

	deltaMsRaw := uint32((target.Time - anchor.Time) * 1000)

	// Mask to fit within bit constraints
	anchorFreqBits := anchorFreqBin & ((1 << maxFreqBits) - 1) // 9 bits
	targetFreqBits := targetFreqBin & ((1 << maxFreqBits) - 1) // 9 bits
	deltaBits := deltaMsRaw & ((1 << maxDeltaBits) - 1)        // 14 bits (max ~16 seconds)

	// Combine into 32-bit address
	address := (anchorFreqBits << 23) | (targetFreqBits << 14) | deltaBits

	return address
}

func FingerprintAudio(songFilePath string, songID uint32) (map[uint32][]models.Couple, error) {
	wavFilePath, err := wav.ConvertToWAV(songFilePath)
	if err != nil {
		return nil, fmt.Errorf("error converting input file to WAV: %v", err)
	}

	wavInfo, err := wav.ReadWavInfo(wavFilePath)
	if err != nil {
		return nil, fmt.Errorf("error reading WAV info: %v", err)
	}

	// Mix to mono: average L+R if stereo, use L directly if already mono.
	// Recognition samples from the browser are always mono, so storing fingerprints
	// from a mono mix ensures every DB address is reachable by a real recording.
	samples := wavInfo.LeftChannelSamples
	if wavInfo.Channels == 2 {
		mixed := make([]float64, len(wavInfo.LeftChannelSamples))
		for i := range mixed {
			mixed[i] = (wavInfo.LeftChannelSamples[i] + wavInfo.RightChannelSamples[i]) / 2
		}
		samples = mixed
	}

	spectro, err := Spectrogram(samples, wavInfo.SampleRate)
	if err != nil {
		return nil, fmt.Errorf("error creating spectrogram: %v", err)
	}

	peaks := ExtractPeaks(spectro, wavInfo.Duration, wavInfo.SampleRate)
	fingerprint := make(map[uint32][]models.Couple)
	for addr, couples := range Fingerprint(peaks, songID) {
		fingerprint[addr] = append(fingerprint[addr], couples...)
	}

	return fingerprint, nil
}
