package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxWorkers = 5

var musicExts = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".wma": true, ".ape": true,
}

const apiBase = "https://api.ygking.top"

type QQSinger struct {
	Name string `json:"name"`
	Mid  string `json:"mid"`
}

type QQAlbum struct {
	Mid  string `json:"mid"`
	Name string `json:"name"`
	Pmid string `json:"pmid"`
}

type QQSong struct {
	Mid    string     `json:"mid"`
	Name   string     `json:"name"`
	Title  string     `json:"title"`
	Singer []QQSinger `json:"singer"`
	Album  QQAlbum    `json:"album"`
}

type QQSearchResult struct {
	Code int `json:"code"`
	Data struct {
		List []QQSong `json:"list"`
	} `json:"data"`
}

type QQLyricResult struct {
	Code int `json:"code"`
	Data struct {
		Lyric string `json:"lyric"`
		Trans string `json:"trans"`
		Roma  string `json:"roma"`
	} `json:"data"`
}

type QQCoverResult struct {
	Code int `json:"code"`
	Data struct {
		URL    string `json:"url"`
		Source string `json:"source"`
		Size   int    `json:"size"`
	} `json:"data"`
}

type Download struct {
	Music   string
	Target  string
	Source  string
	Lyrics  string
	SongMid string
	Status  string
}

type CoverDownload struct {
	Music     string
	Target    string
	Source    string
	CoverURL  string
	Status    string
}

type LocalMatch struct {
	Music  string
	Lyric  string
	Target string
	Score  float64
	Status string
}

func normalizeName(name string) string {
	text := strings.ToLower(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)))
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|（[^）]*）`),
		regexp.MustCompile(`(?i)\b(flac|mp3|wav|m4a|aac|ogg|opus|wma|ape|hq|sq|hires|lyrics?)\b`),
		regexp.MustCompile(`[-_.,，。·•|/\\]+`),
		regexp.MustCompile(`\s+`),
	} {
		if re.String() == `\s+` {
			text = re.ReplaceAllString(text, " ")
		} else {
			text = re.ReplaceAllString(text, " ")
		}
	}
	return strings.TrimSpace(text)
}

func parseTrackName(path string) (title string, artist string) {
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	stem = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)|（[^）]*）`).ReplaceAllString(stem, " ")
	stem = regexp.MustCompile(`\s+`).ReplaceAllString(strings.TrimSpace(stem), " ")
	parts := regexp.MustCompile(`\s+-\s+|\s+–\s+|\s+—\s+`).Split(stem, 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1]), strings.TrimSpace(parts[0])
	}
	return stem, ""
}

func hasLRCTimestamps(text string) bool {
	return regexp.MustCompile(`\[\d{1,2}:\d{2}(?:[.:]\d{1,3})?\]`).MatchString(text)
}

func listFiles(folder string, recursive bool) ([]string, error) {
	var files []string
	if recursive {
		err := filepath.WalkDir(folder, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				files = append(files, path)
			}
			return nil
		})
		sort.Strings(files)
		return files, err
	}

	entries, err := os.ReadDir(folder)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			files = append(files, filepath.Join(folder, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func httpGetText(rawURL string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "local-lrc-downloader/1.0 (personal music library tool)")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(body), "\ufeff"), nil
}

func httpDownloadFile(rawURL, destPath string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "local-lrc-downloader/1.0 (personal music library tool)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(body) < 100 {
		return fmt.Errorf("response too small: %d bytes", len(body))
	}
	return os.WriteFile(destPath, body, 0644)
}

func sameArtist(found string, expected string) bool {
	if expected == "" {
		return true
	}
	splitRe := regexp.MustCompile(`\s*/\s*|\s*,\s*|\s*&\s*`)
	foundParts := splitRe.Split(found, -1)
	expectedParts := splitRe.Split(expected, -1)
	for _, ep := range expectedParts {
		expectedName := normalizeName(ep)
		for _, fp := range foundParts {
			if normalizeName(fp) == expectedName {
				return true
			}
		}
	}
	return normalizeName(found) == normalizeName(expected)
}

func searchQQMusic(title, artist string) (*QQSong, error) {
	query := url.Values{
		"keyword": {title},
		"type":    {"song"},
		"num":     {"20"},
	}
	searchURL := apiBase + "/api/search?" + query.Encode()
	text, err := httpGetText(searchURL)
	if err != nil {
		return nil, err
	}

	var result QQSearchResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, err
	}
	if result.Code != 0 {
		return nil, fmt.Errorf("API error: code %d", result.Code)
	}

	for i := range result.Data.List {
		song := &result.Data.List[i]
		if normalizeName(song.Name) != normalizeName(title) {
			continue
		}
		if artist != "" {
			matched := false
			for _, s := range song.Singer {
				if sameArtist(s.Name, artist) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		return song, nil
	}
	return nil, nil
}

func searchQQMusicBoth(title, artist string) (*QQSong, error) {
	song, err := searchQQMusic(title, artist)
	if err == nil && song != nil {
		return song, nil
	}
	if artist != "" {
		song, err = searchQQMusic(artist, title)
		if err == nil && song != nil {
			return song, nil
		}
		song, err = searchQQMusic(title, "")
		if err == nil && song != nil {
			return song, nil
		}
		song, err = searchQQMusic(artist, "")
		if err == nil && song != nil {
			return song, nil
		}
	}
	return nil, nil
}

func getLyricsQQ(mid string) (string, error) {
	query := url.Values{
		"mid": {mid},
	}
	lyricURL := apiBase + "/api/lyric?" + query.Encode()
	text, err := httpGetText(lyricURL)
	if err != nil {
		return "", err
	}

	var result QQLyricResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("API error: code %d", result.Code)
	}
	return result.Data.Lyric, nil
}

func getCoverURLQQ(mid string) (string, error) {
	query := url.Values{
		"mid":  {mid},
		"size": {"500"},
	}
	coverURL := apiBase + "/api/song/cover?" + query.Encode()
	text, err := httpGetText(coverURL)
	if err != nil {
		return "", err
	}

	var result QQCoverResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return "", err
	}
	if result.Code != 0 {
		return "", fmt.Errorf("API error: code %d", result.Code)
	}
	return result.Data.URL, nil
}

func searchLrcCx(title, artist string) (string, error) {
	query := url.Values{
		"title":  {title},
		"artist": {artist},
	}
	searchURL := "https://api.lrc.cx/lyrics?" + query.Encode()
	text, err := httpGetText(searchURL)
	if err != nil {
		return "", err
	}

	if strings.HasPrefix(text, "{") && strings.Contains(text, "\"lyrics\"") {
		var res struct {
			Lyrics string `json:"lyrics"`
		}
		if err := json.Unmarshal([]byte(text), &res); err == nil && res.Lyrics != "" {
			text = res.Lyrics
		}
	}

	if hasLRCTimestamps(text) {
		return text, nil
	}
	return "", nil
}

func BuildDownloads(folder string, recursive bool, overwrite bool, logFn func(string)) ([]Download, error) {
	files, err := listFiles(folder, recursive)
	if err != nil {
		return nil, err
	}
	var musicFiles []string
	for _, f := range files {
		if musicExts[strings.ToLower(filepath.Ext(f))] {
			musicFiles = append(musicFiles, f)
		}
	}
	results := make([]Download, len(musicFiles))
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for i, music := range musicFiles {
		wg.Add(1)
		go func(idx int, m string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = searchLyricsForFile(m, overwrite, logFn)
		}(i, music)
	}
	wg.Wait()
	return results, nil
}

func searchLyricsForFile(music string, overwrite bool, logFn func(string)) Download {
	target := strings.TrimSuffix(music, filepath.Ext(music)) + ".lrc"
	if _, err := os.Stat(target); err == nil && !overwrite {
		return Download{Music: music, Target: target, Status: "已存在"}
	}
	title, artist := parseTrackName(music)
	if logFn != nil {
		logFn(fmt.Sprintf("搜索中：%s - %s", artist, title))
	}
	var lyrics string
	var source string
	var songMid string
	var found bool
	song, err := searchQQMusicBoth(title, artist)
	if err == nil && song != nil {
		lrcText, err := getLyricsQQ(song.Mid)
		if err == nil && hasLRCTimestamps(lrcText) {
			lyrics = lrcText
			singerName := ""
			if len(song.Singer) > 0 {
				singerName = song.Singer[0].Name
			}
			source = fmt.Sprintf("QQ音乐 - %s - %s", singerName, song.Name)
			songMid = song.Mid
			found = true
		}
	}
	if !found {
		lrcText, err := searchLrcCx(title, artist)
		if err == nil && lrcText != "" {
			lyrics = lrcText
			source = "api.lrc.cx"
			found = true
		}
	}
	if !found && artist != "" {
		lrcText, err := searchLrcCx(artist, title)
		if err == nil && lrcText != "" {
			lyrics = lrcText
			source = "api.lrc.cx"
			found = true
		}
	}
	if !found {
		return Download{Music: music, Target: target, Status: "未找到"}
	}
	return Download{Music: music, Target: target, Source: source, Lyrics: lyrics, SongMid: songMid, Status: "待下载"}
}

func ApplyDownloads(downloads []Download, overwrite bool, logFn func(string)) error {
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i := range downloads {
		if downloads[i].Status != "待下载" {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := downloads[idx]
			if _, err := os.Stat(item.Target); err == nil && !overwrite {
				if logFn != nil {
					logFn(fmt.Sprintf("跳过：%s 已存在", filepath.Base(item.Target)))
				}
				return
			}
			if err := os.WriteFile(item.Target, []byte(strings.TrimSpace(item.Lyrics)+"\n"), 0644); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				return
			}
			if logFn != nil {
				logFn(fmt.Sprintf("已下载：%s", filepath.Base(item.Target)))
			}
		}(i)
	}
	wg.Wait()
	return firstErr
}

func BuildCoverDownloads(folder string, recursive bool, overwrite bool, logFn func(string)) ([]CoverDownload, error) {
	files, err := listFiles(folder, recursive)
	if err != nil {
		return nil, err
	}
	var musicFiles []string
	for _, f := range files {
		if musicExts[strings.ToLower(filepath.Ext(f))] {
			musicFiles = append(musicFiles, f)
		}
	}
	results := make([]CoverDownload, len(musicFiles))
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for i, music := range musicFiles {
		wg.Add(1)
		go func(idx int, m string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[idx] = searchCoverForFile(m, overwrite, logFn)
		}(i, music)
	}
	wg.Wait()
	return results, nil
}

func searchCoverForFile(music string, overwrite bool, logFn func(string)) CoverDownload {
	target := strings.TrimSuffix(music, filepath.Ext(music)) + ".jpg"
	if _, err := os.Stat(target); err == nil && !overwrite {
		return CoverDownload{Music: music, Target: target, Status: "已存在"}
	}
	title, artist := parseTrackName(music)
	if logFn != nil {
		logFn(fmt.Sprintf("搜索封面：%s - %s", artist, title))
	}
	song, err := searchQQMusicBoth(title, artist)
	if err != nil || song == nil {
		return CoverDownload{Music: music, Target: target, Status: "未找到"}
	}
	coverURL, err := getCoverURLQQ(song.Mid)
	if err != nil || coverURL == "" {
		return CoverDownload{Music: music, Target: target, Status: "未找到"}
	}
	singerName := ""
	if len(song.Singer) > 0 {
		singerName = song.Singer[0].Name
	}
	source := fmt.Sprintf("QQ音乐 - %s - %s", singerName, song.Name)
	return CoverDownload{Music: music, Target: target, Source: source, CoverURL: coverURL, Status: "待下载"}
}

func ApplyCoverDownloads(downloads []CoverDownload, overwrite bool, logFn func(string)) error {
	sem := make(chan struct{}, maxWorkers)
	var wg sync.WaitGroup
	for i := range downloads {
		if downloads[i].Status != "待下载" {
			continue
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			item := downloads[idx]
			if _, err := os.Stat(item.Target); err == nil && !overwrite {
				if logFn != nil {
					logFn(fmt.Sprintf("跳过：%s 已存在", filepath.Base(item.Target)))
				}
				return
			}
			if err := httpDownloadFile(item.CoverURL, item.Target); err != nil {
				if logFn != nil {
					logFn(fmt.Sprintf("失败：%s (%v)", filepath.Base(item.Target), err))
				}
				return
			}
			if logFn != nil {
				logFn(fmt.Sprintf("已下载：%s", filepath.Base(item.Target)))
			}
		}(i)
	}
	wg.Wait()
	return nil
}

func similarity(a string, b string) float64 {
	aRunes, bRunes := []rune(a), []rune(b)
	if len(aRunes) == 0 || len(bRunes) == 0 {
		return 0
	}
	dp := make([][]int, len(aRunes)+1)
	for i := range dp {
		dp[i] = make([]int, len(bRunes)+1)
	}
	for i := 1; i <= len(aRunes); i++ {
		for j := 1; j <= len(bRunes); j++ {
			if aRunes[i-1] == bRunes[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else if dp[i-1][j] > dp[i][j-1] {
				dp[i][j] = dp[i-1][j]
			} else {
				dp[i][j] = dp[i][j-1]
			}
		}
	}
	return float64(2*dp[len(aRunes)][len(bRunes)]) / float64(len(aRunes)+len(bRunes))
}

func BuildLocalMatches(folder string, recursive bool, threshold float64, overwrite bool) ([]LocalMatch, error) {
	files, err := listFiles(folder, recursive)
	if err != nil {
		return nil, err
	}
	var musicFiles, lyricFiles []string
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file))
		if musicExts[ext] {
			musicFiles = append(musicFiles, file)
		} else if ext == ".lrc" {
			lyricFiles = append(lyricFiles, file)
		}
	}
	used := map[string]bool{}
	var matches []LocalMatch
	for _, music := range musicFiles {
		target := strings.TrimSuffix(music, filepath.Ext(music)) + ".lrc"
		if _, err := os.Stat(target); err == nil && !overwrite {
			matches = append(matches, LocalMatch{Music: music, Lyric: target, Target: target, Score: 1, Status: "已存在"})
			used[target] = true
			continue
		}
		bestScore := 0.0
		bestLyric := ""
		for _, lyric := range lyricFiles {
			if used[lyric] {
				continue
			}
			score := similarity(normalizeName(music), normalizeName(lyric))
			if score > bestScore {
				bestScore = score
				bestLyric = lyric
			}
		}
		if bestLyric == "" || bestScore < threshold {
			matches = append(matches, LocalMatch{Music: music, Target: target, Status: "未匹配"})
			continue
		}
		used[bestLyric] = true
		matches = append(matches, LocalMatch{Music: music, Lyric: bestLyric, Target: target, Score: bestScore, Status: "待处理"})
	}
	return matches, nil
}

func ApplyLocalMatches(matches []LocalMatch, overwrite bool, logFn func(string)) error {
	for _, item := range matches {
		if item.Status != "待处理" {
			continue
		}
		if _, err := os.Stat(item.Target); err == nil && !overwrite {
			msg := fmt.Sprintf("跳过：%s 已存在", filepath.Base(item.Target))
			if logFn != nil {
				logFn(msg)
			}
			continue
		}
		if err := os.Rename(item.Lyric, item.Target); err != nil {
			return err
		}
		msg := fmt.Sprintf("已重命名：%s -> %s", filepath.Base(item.Lyric), filepath.Base(item.Target))
		if logFn != nil {
			logFn(msg)
		}
	}
	return nil
}
