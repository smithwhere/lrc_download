package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	download := flag.Bool("download", false, "从QQ音乐联网搜索并下载歌词")
	cover := flag.Bool("cover", false, "从QQ音乐联网搜索并下载专辑图片")
	apply := flag.Bool("apply", false, "执行修改；不加时只预览")
	recursive := flag.Bool("recursive", false, "包含子文件夹")
	overwrite := flag.Bool("overwrite", false, "允许覆盖已存在的同名文件")
	threshold := flag.Float64("threshold", 0.72, "最低匹配相似度，默认 0.72")
	noGui := flag.Bool("nogui", false, "强制使用命令行模式")

	flag.Parse()

	if *noGui || *download || *cover || flag.NArg() > 0 || *apply {
		runCLI(download, cover, apply, recursive, overwrite, threshold)
	} else {
		StartGUI()
	}
}

func runCLI(download, cover, apply, recursive, overwrite *bool, threshold *float64) {
	folder := "."
	if flag.NArg() > 0 {
		folder = flag.Arg(0)
	}
	absFolder, err := filepath.Abs(folder)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if *download {
		downloads, err := BuildDownloads(absFolder, *recursive, *overwrite, nil)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		if len(downloads) == 0 {
			fmt.Println("没有找到音乐文件。")
		}
		for _, item := range downloads {
			s := item.Source
			if s == "" {
				s = "-"
			}
			fmt.Printf("[%s] %s  <=  %s\n", item.Status, filepath.Base(item.Music), s)
		}
		if !*apply {
			fmt.Println("\n这是预览结果。确认无误后，加上 --apply 执行。")
			fmt.Println("例：match_lrc.exe --download --apply")
		} else {
			if err := ApplyDownloads(downloads, *overwrite, nil); err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		}
		if !*cover {
			return
		}
		fmt.Println()
	}

	if *cover {
		coverDownloads, err := BuildCoverDownloads(absFolder, *recursive, *overwrite, nil)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		if len(coverDownloads) == 0 {
			fmt.Println("没有找到音乐文件。")
		}
		for _, item := range coverDownloads {
			s := item.Source
			if s == "" {
				s = "-"
			}
			fmt.Printf("[%s] %s  <=  %s\n", item.Status, filepath.Base(item.Music), s)
		}
		if !*apply {
			fmt.Println("\n这是预览结果。确认无误后，加上 --apply 执行。")
			fmt.Println("例：match_lrc.exe --cover --apply")
			return
		}
		if err := ApplyCoverDownloads(coverDownloads, *overwrite, nil); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return
	}

	matches, err := BuildLocalMatches(absFolder, *recursive, *threshold, *overwrite)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if len(matches) == 0 {
		fmt.Println("没有找到音乐文件。")
	}
	for _, item := range matches {
		lyric := "-"
		if item.Lyric != "" {
			lyric = filepath.Base(item.Lyric)
		}
		fmt.Printf("[%s] %s  <=  %s  相似度: %.0f%%\n", item.Status, filepath.Base(item.Music), lyric, item.Score*100)
	}
	if !*apply {
		fmt.Println("\n这是预览结果。确认无误后，加上 --apply 执行。")
		fmt.Println("例：match_lrc.exe --apply")
		return
	}
	if err := ApplyLocalMatches(matches, *overwrite, nil); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
