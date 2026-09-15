package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
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

const qqSearchURL = "https://u.y.qq.com/cgi-bin/musicu.fcg"
const qqLyricURL = "http://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg"

var qqHeaders = map[string]string{
	"Referer":    "https://y.qq.com",
	"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/106.0.0.0 Safari/537.36",
	"Cookie":     "uin=",
}

type Song struct {
	SongMID  string
	Title    string
	Artist   string
	AlbumMID string
	Album    string
}

type Download struct {
	Music  string
	Target string
	Source string
	Lyrics string
	SongID string
	Status string
}

type CoverDownload struct {
	Music    string
	Target   string
	Source   string
	CoverURL string
	Status   string
}

type LocalMatch struct {
	Music  string
	Lyric  string
	Target string
	Score  float64
	Status string
}

type qqSearchResult struct {
	Req1 struct {
		Code int `json:"code"`
		Data struct {
			Meta struct {
				Sum int `json:"sum"`
			} `json:"meta"`
			Body struct {
				Song struct {
					List []struct {
						MID    string `json:"mid"`
						Title  string `json:"title"`
						Singer []struct {
							Name string `json:"name"`
						} `json:"singer"`
						Album struct {
							MID   string `json:"mid"`
							Title string `json:"title"`
						} `json:"album"`
					} `json:"list"`
				} `json:"song"`
			} `json:"body"`
		} `json:"data"`
	} `json:"req_1"`
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

func setQQHeaders(req *http.Request) {
	for k, v := range qqHeaders {
		req.Header.Set(k, v)
	}
}

func qqMusicGet(rawURL string) (string, error) {
	for attempt := 0; attempt < 3; attempt++ {
		text, err := qqMusicGetOnce(rawURL)
		if err == nil {
			return text, nil
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	return "", fmt.Errorf("request failed after retries")
}

func qqMusicGetOnce(rawURL string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", err
	}
	setQQHeaders(req)
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

func qqMusicSearch(query, filter string, requireAlbumMID bool) (*Song, error) {
	payload := map[string]interface{}{
		"comm": map[string]interface{}{
			"ct":         23,
			"cv":         0,
			"g_tk":       5381,
			"uin":        123456,
			"format":     "json",
			"inCharset":  "utf-8",
			"outCharset": "utf-8",
			"notice":     0,
			"platform":   "h5",
			"needNewCode": 1,
		},
		"req_1": map[string]interface{}{
			"method": "DoSearchForQQMusicDesktop",
			"module": "music.search.SearchCgiService",
			"param": map[string]interface{}{
				"num_per_page": 20,
				"page_num":     1,
				"query":        query,
				"search_type":  0,
			},
		},
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	searchURL := qqSearchURL + "?data=" + string(jsonData)
	text, err := qqMusicGet(searchURL)
	if err != nil {
		return nil, err
	}
	var result qqSearchResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, err
	}
	if result.Req1.Code != 0 {
		return nil, fmt.Errorf("QQ音乐API返回错误码: %d", result.Req1.Code)
	}
	for _, item := range result.Req1.Data.Body.Song.List {
		if normalizeName(item.Title) != normalizeName(query) {
			continue
		}
		artistName := ""
		for i, s := range item.Singer {
			if i > 0 {
				artistName += ", "
			}
			artistName += s.Name
		}
		if filter != "" && !sameArtist(artistName, filter) {
			continue
		}
		if requireAlbumMID && item.Album.MID == "" {
			continue
		}
		return &Song{
			SongMID:  item.MID,
			Title:    item.Title,
			Artist:   artistName,
			AlbumMID: item.Album.MID,
			Album:    item.Album.Title,
		}, nil
	}
	return nil, nil
}

func searchMusic(query, filter string) (*Song, error) {
	return qqMusicSearch(query, filter, false)
}

func searchMusicWithCover(query, filter string) (*Song, error) {
	return qqMusicSearch(query, filter, true)
}

func searchMusicBoth(title, artist string) (*Song, error) {
	song, err := searchMusic(title, artist)
	if err == nil && song != nil {
		return song, nil
	}
	if artist != "" {
		song, err = searchMusic(artist, title)
		if err == nil && song != nil {
			return song, nil
		}
		song, err = searchMusic(title, "")
		if err == nil && song != nil {
			return song, nil
		}
		song, err = searchMusic(artist, "")
		if err == nil && song != nil {
			return song, nil
		}
	}
	return nil, nil
}

func searchMusicBothWithCover(title, artist string) (*Song, error) {
	song, err := searchMusicWithCover(title, artist)
	if err == nil && song != nil {
		return song, nil
	}
	if artist != "" {
		song, err = searchMusicWithCover(artist, title)
		if err == nil && song != nil {
			return song, nil
		}
		song, err = searchMusicWithCover(title, "")
		if err == nil && song != nil {
			return song, nil
		}
		song, err = searchMusicWithCover(artist, "")
		if err == nil && song != nil {
			return song, nil
		}
	}
	return nil, nil
}

func qqMusicGetLyrics(songmid string) (string, error) {
	lyricURL := fmt.Sprintf("%s?songmid=%s&pcachetime=%d&g_tk=5381&loginUin=0&hostUin=0&inCharset=utf8&outCharset=utf-8&notice=0&platform=yqq&needNewCode=0",
		qqLyricURL, songmid, time.Now().UnixMilli())
	text, err := qqMusicGet(lyricURL)
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"callback(", "MusicJsonCallback(", "jsonCallback("} {
		text = strings.TrimPrefix(text, prefix)
	}
	text = strings.TrimSuffix(text, ")")
	var res struct {
		Lyric string `json:"lyric"`
	}
	if err := json.Unmarshal([]byte(text), &res); err != nil {
		return "", err
	}
	if res.Lyric == "" {
		return "", nil
	}
	decoded, err := base64.StdEncoding.DecodeString(res.Lyric)
	if err != nil {
		return "", err
	}
	return html.UnescapeString(string(decoded)), nil
}

func qqMusicCoverURL(albummid string) string {
	if albummid == "" {
		return ""
	}
	return fmt.Sprintf("https://y.gtimg.cn/music/photo_new/T002R800x800M000%s.jpg", albummid)
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
	song, err := searchMusicBoth(title, artist)
	if err == nil && song != nil {
		lrcText, err := qqMusicGetLyrics(song.SongMID)
		if err == nil && hasLRCTimestamps(lrcText) {
			return Download{
				Music:  music,
				Target: target,
				Source: fmt.Sprintf("QQ音乐 - %s - %s", song.Artist, song.Title),
				Lyrics: lrcText,
				SongID: song.SongMID,
				Status: "待下载",
			}
		}
	}
	return Download{Music: music, Target: target, Status: "未找到"}
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
	song, err := searchMusicBothWithCover(title, artist)
	if err != nil || song == nil {
		return CoverDownload{Music: music, Target: target, Status: "未找到"}
	}
	coverURL := qqMusicCoverURL(song.AlbumMID)
	if coverURL == "" {
		return CoverDownload{Music: music, Target: target, Status: "未找到"}
	}
	return CoverDownload{
		Music:    music,
		Target:   target,
		Source:   fmt.Sprintf("QQ音乐 - %s - %s", song.Artist, song.Title),
		CoverURL: coverURL,
		Status:   "待下载",
	}
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
