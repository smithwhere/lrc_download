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
	"time"
)

var musicExts = map[string]bool{
	".mp3": true, ".flac": true, ".wav": true, ".m4a": true, ".aac": true,
	".ogg": true, ".opus": true, ".wma": true, ".ape": true,
}

type MetingItem struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	LRC    string `json:"lrc"`
}

type Download struct {
	Music  string
	Target string
	Source string
	Lyrics string
	Status string
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

func sameArtist(found string, expected string) bool {
	if expected == "" {
		return true
	}
	expectedName := normalizeName(expected)
	for _, part := range regexp.MustCompile(`\s*/\s*|\s*,\s*|\s*&\s*`).Split(found, -1) {
		if normalizeName(part) == expectedName {
			return true
		}
	}
	return normalizeName(found) == expectedName
}

func searchMeting(title string, artist string) (*MetingItem, string, error) {
	query := url.Values{
		"server": {"netease"},
		"type":   {"search"},
		"id":     {title},
	}
	searchURL := "https://meting.mikus.ink/api?" + query.Encode()
	text, err := httpGetText(searchURL)
	if err != nil {
		return nil, "", err
	}

	var items []MetingItem
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, "", err
	}

	for _, item := range items {
		if normalizeName(item.Title) != normalizeName(title) {
			continue
		}
		if !sameArtist(item.Author, artist) {
			continue
		}
		if item.LRC == "" {
			continue
		}
		lyrics, err := httpGetText(item.LRC)
		if err != nil {
			continue
		}
		if hasLRCTimestamps(lyrics) {
			return &item, lyrics, nil
		}
	}
	return nil, "", nil
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

	// 兼容处理：有些 API 可能返回 {"lyrics": "..."} JSON
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
	var downloads []Download
	for _, music := range files {
		if !musicExts[strings.ToLower(filepath.Ext(music))] {
			continue
		}
		target := strings.TrimSuffix(music, filepath.Ext(music)) + ".lrc"
		if _, err := os.Stat(target); err == nil && !overwrite {
			downloads = append(downloads, Download{Music: music, Target: target, Status: "已存在"})
			continue
		}
		title, artist := parseTrackName(music)
		if logFn != nil {
			logFn(fmt.Sprintf("搜索中：%s - %s", artist, title))
		}

		var lyrics string
		var source string
		var found bool

		// 尝试第一个源：Meting/网易云
		item, lrcText, err := searchMeting(title, artist)
		if err == nil && item != nil {
			lyrics = lrcText
			source = fmt.Sprintf("Meting/NetEase - %s - %s", item.Author, item.Title)
			found = true
		}

		// 如果没找到，尝试备用源：lrc.cx
		if !found {
			lrcText, err := searchLrcCx(title, artist)
			if err == nil && lrcText != "" {
				lyrics = lrcText
				source = "api.lrc.cx"
				found = true
			}
		}

		if !found {
			downloads = append(downloads, Download{Music: music, Target: target, Status: "未找到"})
			continue
		}

		downloads = append(downloads, Download{Music: music, Target: target, Source: source, Lyrics: lyrics, Status: "待下载"})
	}
	return downloads, nil
}

func ApplyDownloads(downloads []Download, overwrite bool, logFn func(string)) error {
	for _, item := range downloads {
		if item.Status != "待下载" {
			continue
		}
		if _, err := os.Stat(item.Target); err == nil && !overwrite {
			msg := fmt.Sprintf("跳过：%s 已存在", filepath.Base(item.Target))
			if logFn != nil {
				logFn(msg)
			}
			continue
		}
		if err := os.WriteFile(item.Target, []byte(strings.TrimSpace(item.Lyrics)+"\n"), 0644); err != nil {
			return err
		}
		msg := fmt.Sprintf("已下载：%s", filepath.Base(item.Target))
		if logFn != nil {
			logFn(msg)
		}
	}
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

func ApplyLocalMatches(matches []LocalMatch, copyMode bool, overwrite bool, logFn func(string)) error {
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
		if copyMode {
			data, err := os.ReadFile(item.Lyric)
			if err != nil {
				return err
			}
			if err := os.WriteFile(item.Target, data, 0644); err != nil {
				return err
			}
			msg := fmt.Sprintf("已复制：%s -> %s", filepath.Base(item.Lyric), filepath.Base(item.Target))
			if logFn != nil {
				logFn(msg)
			}
		} else {
			if err := os.Rename(item.Lyric, item.Target); err != nil {
				return err
			}
			msg := fmt.Sprintf("已重命名：%s -> %s", filepath.Base(item.Lyric), filepath.Base(item.Target))
			if logFn != nil {
				logFn(msg)
			}
		}
	}
	return nil
}
