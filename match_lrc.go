package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	download := flag.Bool("download", false, "从 Meting/网易云音乐联网搜索并下载歌词")
	apply := flag.Bool("apply", false, "执行修改；不加时只预览")
	copyMode := flag.Bool("copy", false, "复制歌词文件；默认重命名歌词文件")
	recursive := flag.Bool("recursive", false, "包含子文件夹")
	overwrite := flag.Bool("overwrite", false, "允许覆盖已存在的同名 .lrc")
	threshold := flag.Float64("threshold", 0.72, "最低匹配相似度，默认 0.72")
	noGui := flag.Bool("nogui", false, "强制使用命令行模式")
	flag.Parse()

	// 如果没有提供任何命令行参数，或者显式指定了 --gui (默认逻辑)，则启动 GUI
	// 只有当提供了特定的操作标志（如 --download 或 arg[0]）且没有 --nogui 时，才可能进入 CLI
	// 为了方便用户，我们约定：如果没有 flags 也没有 args，启动 GUI
	if flag.NFlag() == 0 && flag.NArg() == 0 {
		StartGUI()
		return
	}
	
	// 如果用户手动加了 --nogui 或者是带了参数运行，走 CLI 逻辑
	if *noGui || *download || flag.NArg() > 0 || *apply {
		runCLI(download, apply, copyMode, recursive, overwrite, threshold)
	} else {
		StartGUI()
	}
}

func runCLI(download *bool, apply *bool, copyMode *bool, recursive *bool, overwrite *bool, threshold *float64) {
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
			source := item.Source
			if source == "" {
				source = "-"
			}
			fmt.Printf("[%s] %s  <=  %s\n", item.Status, filepath.Base(item.Music), source)
		}
		if !*apply {
			fmt.Println("\n这是预览结果。确认无误后，加上 --apply 执行。")
			fmt.Println("例：match_lrc.exe --download --apply")
			return
		}
		if err := ApplyDownloads(downloads, *overwrite, nil); err != nil {
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
		fmt.Println("例：match_lrc.exe --apply --copy")
		return
	}
	if err := ApplyLocalMatches(matches, *copyMode, *overwrite, nil); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
