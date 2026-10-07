package slideshow

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	//go:embed logo.png
	embeddedLogo []byte

	maxSlideshowTime = time.Minute * 5 // switch to "default" slides after this long without a new match
)

// Slideshow plays a feh slideshow keyed off a matched song title, with
// images expected under <parentDir>/<subdirectory>/, where subdirectory
// is songMap[title] if the title is mapped, or "default" otherwise.
type Slideshow struct {
	parentDir     string
	logoFrequency int
	interval      int
	songMap       map[string]string // title -> slideshow subdirectory name

	startChan chan struct{}
	songChan  chan string
}

// New prepares a Slideshow rooted at parentDir, using songMap (title ->
// slideshow subdirectory name, see songs.LoadFile) to resolve a matched
// title to its images. It fails fast if parentDir doesn't exist or the
// embedded logo can't be staged, rather than discovering that a few
// iterations into Run.
func New(parentDir string, songMap map[string]string, logoFrequency, interval int) (*Slideshow, error) {
	parentDir = expandPath(parentDir)

	info, err := os.Stat(parentDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("parent image directory not found: %s", parentDir)
	}

	return &Slideshow{
		parentDir:     parentDir,
		logoFrequency: logoFrequency,
		interval:      interval,
		songMap:       songMap,
		startChan:     make(chan struct{}, 1),
		songChan:      make(chan string, 1),
	}, nil
}

// Show requests a switch to songTitle's slides. Non-blocking: if Run
// isn't keeping up (or was never started), the request is logged and
// dropped rather than stalling the caller — song recognition must never
// be able to block on this.
func (s *Slideshow) Show(songTitle string) {
	select {
	case s.songChan <- songTitle:
	default:
		log.Printf("slideshow busy or unavailable; dropped match for %q", songTitle)
	}
}

func (s *Slideshow) Start() {
	s.startChan <- struct{}{}
}

func (s *Slideshow) Run(ctx context.Context) error {
	<-s.startChan
	fmt.Println("Starting slideshow stream...")

	logo, err := os.Create("songarooni-slideshow-logo.png")
	if err != nil {
		return fmt.Errorf("failed to create logo file: %w", err)
	}
	defer logo.Close()
	err = os.WriteFile(logo.Name(), embeddedLogo, 0644)
	if err != nil {
		return fmt.Errorf("failed to write slide deck file: %w", err)
	}

	imgList, err := os.Create("img-list.txt")
	if err != nil {
		return fmt.Errorf("failed to create slide deck file: %w", err)
	}
	defer imgList.Close()

	absLogoPath, err := filepath.Abs(logo.Name())
	if err != nil {
		return fmt.Errorf("failed to get absolute path of logo: %w", err)
	}
	logoLineLen, err := imgList.WriteString(absLogoPath + "\n")
	if err != nil {
		log.Println("Error writing to img-list.txt:", err)
	}

	// return to default slides after maxSlideshowTime without a new match
	timer := time.NewTimer(maxSlideshowTime)
	defer timer.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				fmt.Println("No new match for a while; switching to default slides")
				s.Show("default")
			}
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case songTitle := <-s.songChan:
				timer.Reset(maxSlideshowTime)

				// get path from song title
				subdir, ok := s.songMap[songTitle]
				if !ok {
					log.Printf("no song map entry for %q; using default slides", songTitle)
					subdir = "default"
				}
				fmt.Printf("Looking for slides in %s/%s\n", s.parentDir, subdir)
				slideDir := filepath.Join(s.parentDir, subdir)
				info, err := os.Stat(slideDir)
				if err != nil || !info.IsDir() {
					// Not an error; just no slides for this song.
					// Play default
					slideDir = filepath.Join(s.parentDir, "default")
				}

				// slide file names
				deck, err := slideDeck(slideDir, logo.Name(), s.logoFrequency)
				if err != nil {
					log.Println("Error getting slides:", err)
					continue
				}

				// Keep the logo line (the first logoLineLen bytes) and
				// overwrite everything after it with the new deck.
				// len(embeddedLogo) is the PNG's byte size, not this
				// line's length, and Seek(0, 0) rewinds to the start of
				// the file — both would clobber the logo line itself.
				if err := imgList.Truncate(int64(logoLineLen)); err != nil {
					log.Println("Error truncating img-list.txt:", err)
				}
				if _, err := imgList.Seek(int64(logoLineLen), 0); err != nil {
					log.Println("Error seeking img-list.txt:", err)
				}
				for _, slide := range deck {
					_, err = imgList.WriteString(slide + "\n")
					if err != nil {
						log.Println("Error writing to img-list.txt:", err)
					}
				}
				if err = imgList.Sync(); err != nil {
					log.Println("Error syncing img-list.txt:", err)
				}

			}
		}
	}()
	fmt.Println(filepath.Abs(imgList.Name()))

	path, err := (filepath.Abs(imgList.Name()))
	if err != nil {
		return fmt.Errorf("failed to get absolute path of img-list.txt: %w", err)
	}
	log.Printf("Using img-list.txt at %s", path)
	args := []string{
		"-F",                                // fullscreen
		"-Z",                                // auto-zoom
		"-D", fmt.Sprintf("%d", s.interval), // slide delay
		"-R", fmt.Sprintf("%d", s.interval), // reload every n seconds
		"--hide-pointer", // hide mouse cursor
		"--quiet",        // suppress warnings
		"-f", path,       // directory to watch
	}

	// feh exiting for any reason (crash, closed window, X restart, ...)
	// must not end Run: the goroutines above are tied to ctx and run for
	// the app's whole lifetime, so returning here on every feh exit would
	// tear down tmpDir out from under them. Only ctx cancellation ends
	// Run; any other feh exit is logged and feh is restarted.
	for {
		execCmd := exec.CommandContext(ctx, "feh", args...)
		runErr := execCmd.Run()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Printf("feh exited unexpectedly (%v); restarting", runErr)
		time.Sleep(time.Second)
	}
}

func slideDeck(imgDir, logoPath string, logoFrequency int) ([]string, error) {
	info, err := os.Stat(imgDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("image directory not found: %s", imgDir)
	}
	var files []string
	err = filepath.Walk(imgDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") {
				return nil // skip hidden files
			}
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" {
				files = append(files, path)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("error scanning directory: %v", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no image files found in directory: %s", imgDir)
	}
	var slideImages []string
	for _, file := range files {
		// skip logo
		if filepath.Clean(file) != filepath.Clean(logoPath) {
			slideImages = append(slideImages, file)
		}
	}

	// randomized, if default. imgDir is a full path (parentDir joined
	// with the subdirectory), so the subdirectory name itself — not the
	// whole path — is what's compared here; comparing the full path
	// against the literal string "default" could never match.
	if strings.ToLower(filepath.Base(imgDir)) == "default" {
		rand.Shuffle(len(slideImages), func(i, j int) {
			slideImages[i], slideImages[j] = slideImages[j], slideImages[i]
		})
	}

	// Not removed here: feh needs to read this file for as long as it's
	// part of the current slide deck. Old copies are cleaned up by the
	// "remove old slides" loop in Run when the deck changes; tmpDir itself
	// is cleaned up by Run's defer on shutdown.

	for i := range slideImages {
		if i%logoFrequency == 0 && i > 0 { // Insert logo after every x images
			absLogoPath, err := filepath.Abs(logoPath)
			if err != nil {
				return nil, fmt.Errorf("failed to get absolute path of logo: %w", err)
			}
			slideImages = append(slideImages[:i], append([]string{absLogoPath}, slideImages[i:]...)...)
		}

	}
	return slideImages, nil
}

func expandPath(path string) string {
	if len(path) >= 2 && path[:2] == "~/" {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
