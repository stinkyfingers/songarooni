package slideshow

import (
	"bufio"
	_ "embed"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	//go:embed logo.png
	embeddedLogo []byte
)

func Run(defaultDrive string, defaultLogoFrequency, defaultInterval int, song <-chan string, stop <-chan struct{}) error {
	parentDir := expandPath(defaultDrive)
	interval := defaultInterval
	logoFrequency := defaultLogoFrequency

	info, err := os.Stat(parentDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("parent image directory not found: %s", parentDir)
	}

	logoPath, err := logo()
	if err != nil {
		return fmt.Errorf("failed to prepare logo: %v", err)
	}

	defer os.Remove(logoPath)

	p := &player{}
	defer p.stop()

	for {
		select {
		case songTitle := <-song:
			log.Printf("Received song title: %s", songTitle)
			slideDir := filepath.Join(parentDir, songTitle)
			info, err := os.Stat(slideDir)
			if err != nil || !info.IsDir() {
				// Not an error; just no slides for this song.
				// Leave whatever is currently playing alone.
				continue
			}
			slides, err := getSlides(slideDir, logoPath, logoFrequency)
			if err != nil {
				log.Printf("failed to get slides for %s: %v", songTitle, err)
				continue
			}

			log.Printf("Starting slideshow for song: %s", songTitle)
			if err := p.switchTo(slides.Name(), float64(interval)); err != nil {
				log.Printf("failed to start slideshow for %s: %v", songTitle, err)
				os.Remove(slides.Name())
			}

		case <-stop:
			log.Println("Stopping slideshow...")
			return nil
		}
	}
}

// player owns the single feh process that's currently showing a
// slideshow, if any. switchTo kills whatever is running (if anything)
// and starts a new one, synchronously and without needing anyone else to
// be listening for a "stop" signal — which is what made the old
// channel-handshake approach deadlock on the very first song match.
type player struct {
	mu           sync.Mutex
	cmd          *exec.Cmd
	playlistPath string
}

// switchTo stops the current slideshow (if any) and starts feh against
// playlistPath. playlistPath is removed automatically the next time the
// slideshow is switched or stopped.
func (p *player) switchTo(playlistPath string, interval float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.stopLocked()

	cmd := exec.Command("feh", fehArgs(playlistPath, interval)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start feh: %w", err)
	}

	p.cmd = cmd
	p.playlistPath = playlistPath

	// Reap the process in the background so a later switchTo/stop isn't
	// blocked waiting for feh to actually exit after being killed.
	go func() {
		if err := cmd.Wait(); err != nil {
			// Expected when we kill it ourselves; only unexpected exits
			// are worth a log line.
			if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != -1 {
				log.Printf("feh exited: %v", err)
			}
		}
	}()

	return nil
}

// stop ends whatever slideshow is currently running, if any.
func (p *player) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *player) stopLocked() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	p.cmd = nil

	if p.playlistPath != "" {
		os.Remove(p.playlistPath)
		p.playlistPath = ""
	}
}

// fehArgs builds the feh CLI arguments for playing playlistPath on a
// fixed interval.
func fehArgs(playlistPath string, interval float64) []string {
	args := []string{
		"-F",                                // fullscreen
		"-Z",                                // auto-zoom
		"-D", fmt.Sprintf("%.1f", interval), // slide delay
		"--hide-pointer",           // hide mouse cursor
		"--quiet",                  // suppress warnings
		"--filelist", playlistPath, // read filepaths from file
	}
	return args
}

func getSlides(imgDir, logoPath string, logoFrequency int) (*os.File, error) {
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
		if filepath.Clean(file) != filepath.Clean(logoPath) {
			slideImages = append(slideImages, file)
		}
	}

	// randomized
	rand.Shuffle(len(slideImages), func(i, j int) {
		slideImages[i], slideImages[j] = slideImages[j], slideImages[i]
	})

	fmt.Printf("Starting slideshow from %s with %d slides (logo alternates every %dth slide)\n", imgDir, len(slideImages), logoFrequency)

	// Write playlist to temp file for feh
	playlistFile, err := os.CreateTemp("", "slideshow-playlist-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp playlist file: %v", err)
	}
	// Not removed here: feh needs to read this file for as long as the
	// slideshow plays. The player that runs feh against it owns cleanup
	// (see player.stopLocked), once it's replaced or the process stops.

	for i, file := range slideImages {
		if i%logoFrequency == 0 { // Insert logo after every x images
			if _, err := playlistFile.WriteString(logoPath + "\n"); err != nil {
				_ = playlistFile.Close()
				return nil, fmt.Errorf("failed to write logo to playlist: %v", err)
			}
		}
		if _, err := playlistFile.WriteString(file + "\n"); err != nil {
			_ = playlistFile.Close()
			return nil, fmt.Errorf("failed to write playlist: %v", err)
		}
	}
	if err := playlistFile.Close(); err != nil {
		return nil, fmt.Errorf("failed to finalize playlist file: %v", err)
	}
	return playlistFile, nil
}

func logo() (string, error) {
	tmpLogo, err := os.CreateTemp("", "slideshow-logo-*.png")
	if err != nil {
		return "", fmt.Errorf("failed to create temp logo file: %v", err)
	}
	if _, err := tmpLogo.Write(embeddedLogo); err != nil {
		_ = tmpLogo.Close()
		return "", fmt.Errorf("failed to write embedded logo: %v", err)
	}
	if err := tmpLogo.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize embedded logo file: %v", err)
	}
	logoPath := tmpLogo.Name()
	return logoPath, nil
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

func prompt(reader *bufio.Reader, question, defaultVal string) string {
	fmt.Printf("%s [%s]: ", question, defaultVal)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultVal
	}
	return input
}
