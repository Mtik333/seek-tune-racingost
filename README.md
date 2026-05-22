<h1 align="center">SeekTune (tweaked for RacingSoundtracks.com) :musical_note:</h1>

<p align="center">
99% of implementation made by <a href="https://github.com/cgzirim">cgzirim</a>. See links below where he explains stuff

<p align="center">
<a href="https://drive.google.com/file/d/1I2esH2U4DtXHsNgYbUi4OL-ukV5i_1PI/view" target="_blank">Demo in Video</a> | <a href="https://www.youtube.com/watch?v=a0CVCcb0RJM" target="_blank">How it was made (YouTube)</a></p>

## Description 🎼
SeekTune is an implementation of Shazam's song recognition algorithm based on insights from these [resources](#resources--card_file_box). 

Original code has been slightly tweaked to get rid of functionalities not useful for me in context of my website - <a href="https://racingsoundtracks.com/content/home">RacingSoundtracks.com</a>. Few commands were also added.

This fork's goal is to fingerprint music from <a href="https://racingsoundtracks.com/content/home">RacingSoundtracks.com</a> that is not released on any kind of streaming service, like Spotify, Deezer, Tidal or Apple Music. This shoud help users to identify songs that Shazam would not give result (or "right result") about

Removed functionalities:
- Building "songs" table
   - I don't need it because my website has already metadata of artists and ID of song
- Client front-end + Wasm
   - Functionality is supposed to be integrated with my website so I'm not going to use this front-end
- Docker scripts
   - Again, for achieving my goal I simply don't need to use that

## Installation :desktop_computer:
### Prerequisites
- Golang: [Install Golang](https://golang.org/dl/)
- FFmpeg: [Install FFmpeg](https://ffmpeg.org/download.html)
- NPM: [Install Node](https://nodejs.org/en/download)
- YT-DLP: [Install YT-DLP](https://github.com/yt-dlp/yt-dlp/wiki/Installation)

### Steps
📦 Clone the repository (either original or this one):
```
git clone https://github.com/Mtik333/seek-tune-racingost.git
cd seek-tune-racingost
```

#### 💻 Set Up Natively
Install dependencies for the backend
```
cd server
go get ./...
```
Install dependencies for the client
```
cd client
npm install
```

## Usage (Native Setup) :bicyclist:

Here I will only list changed or introduced commands + any dependencies

#### ▸ Start the Backend App 🏃‍♀️ 
```
cd server
go run *.go serve [-proto <http|https> (default: http)] [-port <port number> (default: 5000)]
```
I think I used port 5500 despite what's the default value

#### ▸ Compile the app to executable

Seems that this is the command
```
cd server
go build -tags netgo -ldflags '-s -w' -o seek-tune
```
#### ▸ Find "raw" matches for a song/recording based on song-id filter 🔎

```
./seek-tune recognize [-songs] <path_to_csv_with_songids.csv> recording.wav
```
OR
```
go run *.go recognize [-songs] <path_to_csv_with_songids.csv> recording.wav
```
Second argument is a CSV file which consists just of IDs of songs. The reason for this parameter is following: I'm aiming to make website support recognizing song per certain game, or game series. 

For this reason, I want to limit number of fingerprints analyzed to the actual scope of potential search by the user. This also causes this one to show only top 5 matches, in a JSON format because that's easier to communicate with the website

Example CSV is just a plain list of integers, so looks like this:

```
148
149
150
151
152
153
```

Example output
```
./seek-tune recognize drknow_sample3.wav -songs tmp/filter.csv

[{"songId":152,"score":24,"confidence":0.03625377643504532,"timestampMs":135400},{"songId":150,"score":9,"confidence":0.013595166163141994,"timestampMs":132600},{"songId":151,"score":9,"confidence":0.013595166163141994,"timestampMs":121600},{"songId":148,"score":8,"confidence":0.012084592145015106,"timestampMs":99800},{"songId":149,"score":7,"confidence":0.010574018126888218,"timestampMs":127700}]
```

#### ▸ Export fingerprints to database file or CSV file    
There are some possibilities
```
go run *.go export [-o] <path_to_sqlite_db_file.sqlite/csv> [-browser] <browser_name> [-delay] <delay_as_integer> [-jitter] <jitter_integer> [-retries] <retries_integer>
```
OR
```
./seek-tune export [-o] <path_to_sqlite_db_file.sqlite/csv> [-browser] <browser_name> [-delay] <delay_as_integer> [-jitter] <jitter_integer> [-retries] <retries_integer>
```
Export is basically downloading songs from YouTube and generate fingerprints. Depending on "conditions", it can generate SQL file:
```
./seek-tune export songs.csv
```
It can also generate database file (SQLite) when -o parameter is provided
```
./seek-tune export songs.csv -o fingerprints.db
```
Finally it can also export as CSV
```
./seek-tune export songs.csv -o fingerprints.csv
```
Format of songs.csv (input file) is simply a comma separated list of lines, where first column is song_id, and second column is source ID of YouTube video.
```
148,x4EmAnC_DYo
149,RdWwkFSLznM
150,EB1c1A8XJd8
```
If you are afraid that YouTube might block YT-DLP due to fetching data too fast, parameters such as:
- browser
- delay
- jitter
- retries

Should help in preventing that from happening, to make it more look like some user actually trying to stream these resources. I haven't tested much so far, as the time of writing though.

What is important - if you already fingerprinted songs with certain IDs, trying to re-fingerprint will simply make application skip these links and proceed to further ones in the source file.
```
[1/16086] song_id=1        already fingerprinted, skipping
[2/16086] song_id=2        already fingerprinted, skipping
[3/16086] song_id=3        already fingerprinted, skipping
```

## Resources  :card_file_box:
- [How does Shazam work - Coding Geek](https://drive.google.com/file/d/1ahyCTXBAZiuni6RTzHzLoOwwfTRFaU-C/view) (main resource)
- [Song recognition using audio fingerprinting](https://hajim.rochester.edu/ece/sites/zduan/teaching/ece472/projects/2019/AudioFingerprinting.pdf)
- [How does Shazam work - Toptal](https://www.toptal.com/algorithms/shazam-it-music-processing-fingerprinting-and-recognition)
- [Creating Shazam in Java](https://www.royvanrijn.com/blog/2010/06/creating-shazam-in-java/)


## Author (of original code and repository) :black_nib:
- Chigozirim Igweamaka
  - Connect with me on [LinkedIn](https://www.linkedin.com/in/ichigozirim/).
  - Check out my other [GitHub](https://github.com/cgzirim) projects.
  - Follow me on [Twitter](https://twitter.com/cgzirim).
 

## Author (of this abyssmal change) :black_nib:
- Mateusz Walendziuk
  - [LinkedIn](https://www.linkedin.com/in/ichigozirim/).
  - [GitHub](https://github.com/mtik333) projects.


## License :lock:
This project is licensed under the MIT License - see the [LICENSE](./LICENSE) file for details.
