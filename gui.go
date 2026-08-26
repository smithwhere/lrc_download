package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

type MyMainWindow struct {
	*walk.MainWindow
	logView    *walk.TextEdit
	folderEdit *walk.LineEdit
	recursive  *walk.CheckBox
	overwrite  *walk.CheckBox
	threshold  *walk.LineEdit
}

func (mw *MyMainWindow) Log(msg string) {
	mw.WindowBase.Synchronize(func() {
		mw.logView.AppendText(msg + "\r\n")
	})
}

func (mw *MyMainWindow) runDownload() {
	folder := mw.folderEdit.Text()
	if folder == "" {
		walk.MsgBox(mw, "错误", "请先选择文件夹", walk.MsgBoxIconError)
		return
	}
	absFolder, _ := filepath.Abs(folder)
	recursive := mw.recursive.Checked()
	overwrite := mw.overwrite.Checked()

	mw.logView.SetText("")
	mw.Log("开始联网搜索...")

	go func() {
		downloads, err := BuildDownloads(absFolder, recursive, overwrite, mw.Log)
		if err != nil {
			mw.Log("错误: " + err.Error())
			return
		}
		if len(downloads) == 0 {
			mw.Log("没有找到音乐文件。")
			return
		}
		
		count := 0
		for _, item := range downloads {
			if item.Status == "待下载" {
				count++
			}
			mw.Log(fmt.Sprintf("[%s] %s", item.Status, filepath.Base(item.Music)))
		}

		if count > 0 {
			if walk.MsgBox(mw, "确认", fmt.Sprintf("发现 %d 个可下载歌词，是否执行下载？", count), walk.MsgBoxYesNo) == walk.DlgCmdYes {
				err := ApplyDownloads(downloads, overwrite, mw.Log)
				if err != nil {
					mw.Log("下载失败: " + err.Error())
				} else {
					mw.Log("下载完成！")
				}
			}
		} else {
			mw.Log("没有可下载的歌词。")
		}
	}()
}

func (mw *MyMainWindow) runCoverDownload() {
	folder := mw.folderEdit.Text()
	if folder == "" {
		walk.MsgBox(mw, "错误", "请先选择文件夹", walk.MsgBoxIconError)
		return
	}
	absFolder, _ := filepath.Abs(folder)
	recursive := mw.recursive.Checked()
	overwrite := mw.overwrite.Checked()

	mw.logView.SetText("")
	mw.Log("开始搜索专辑图片...")

	go func() {
		downloads, err := BuildCoverDownloads(absFolder, recursive, overwrite, mw.Log)
		if err != nil {
			mw.Log("错误: " + err.Error())
			return
		}
		if len(downloads) == 0 {
			mw.Log("没有找到音乐文件。")
			return
		}

		count := 0
		for _, item := range downloads {
			if item.Status == "待下载" {
				count++
			}
			mw.Log(fmt.Sprintf("[%s] %s", item.Status, filepath.Base(item.Music)))
		}

		if count > 0 {
			if walk.MsgBox(mw, "确认", fmt.Sprintf("发现 %d 个可下载封面，是否执行下载？", count), walk.MsgBoxYesNo) == walk.DlgCmdYes {
				err := ApplyCoverDownloads(downloads, overwrite, mw.Log)
				if err != nil {
					mw.Log("下载失败: " + err.Error())
				} else {
					mw.Log("专辑图片下载完成！")
				}
			}
		} else {
			mw.Log("没有可下载的封面。")
		}
	}()
}

func (mw *MyMainWindow) runLocalMatch() {
	folder := mw.folderEdit.Text()
	if folder == "" {
		walk.MsgBox(mw, "错误", "请先选择文件夹", walk.MsgBoxIconError)
		return
	}
	absFolder, _ := filepath.Abs(folder)
	recursive := mw.recursive.Checked()
	overwrite := mw.overwrite.Checked()
	threshold := 0.72
	fmt.Sscanf(mw.threshold.Text(), "%f", &threshold)

	mw.logView.SetText("")
	mw.Log("开始本地匹配...")

	go func() {
		matches, err := BuildLocalMatches(absFolder, recursive, threshold, overwrite)
		if err != nil {
			mw.Log("错误: " + err.Error())
			return
		}
		if len(matches) == 0 {
			mw.Log("没有找到音乐文件。")
			return
		}

		count := 0
		for _, item := range matches {
			if item.Status == "待处理" {
				count++
			}
			mw.Log(fmt.Sprintf("[%s] %s  相似度 %.0f%%", item.Status, filepath.Base(item.Music), item.Score*100))
		}

		if count > 0 {
			if walk.MsgBox(mw, "确认", fmt.Sprintf("发现 %d 个可匹配歌词，是否执行操作？", count), walk.MsgBoxYesNo) == walk.DlgCmdYes {
				err := ApplyLocalMatches(matches, overwrite, mw.Log)
				if err != nil {
					mw.Log("处理失败: " + err.Error())
				} else {
					mw.Log("本地匹配处理完成！")
				}
			}
		} else {
			mw.Log("没有可匹配的本地歌词。")
		}
	}()
}

func (mw *MyMainWindow) selectFolder() {
	dlg := new(walk.FileDialog)
	dlg.Title = "选择音乐文件夹"
	if ok, err := dlg.ShowBrowseFolder(mw); err != nil {
		return
	} else if !ok {
		return
	}
	mw.folderEdit.SetText(dlg.FilePath)
}

func StartGUI() {
	mw := new(MyMainWindow)

	if err := (MainWindow{
		AssignTo: &mw.MainWindow,
		Title:    "LRC 歌词同步助手 (by 可乐虎)",
		MinSize:  Size{600, 400},
		Layout:   VBox{},
		Children: []Widget{
			Composite{
				Layout: Grid{Columns: 3},
				Children: []Widget{
					Label{Text: "目标文件夹"},
					LineEdit{AssignTo: &mw.folderEdit, Text: "."},
					PushButton{Text: "浏览...", OnClicked: mw.selectFolder},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					CheckBox{AssignTo: &mw.recursive, Text: "递归子文件夹"},
					CheckBox{AssignTo: &mw.overwrite, Text: "覆盖已有文件"},
					Label{Text: "匹配阈值"},
					LineEdit{AssignTo: &mw.threshold, Text: "0.72", MaxLength: 4},
				},
			},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					PushButton{Text: "联网搜索并下载歌词", OnClicked: mw.runDownload},
					PushButton{Text: "下载专辑图片", OnClicked: mw.runCoverDownload},
					PushButton{Text: "本地重命名匹配歌词", OnClicked: mw.runLocalMatch},
				},
			},
			Label{Text: "运行日志:"},
			TextEdit{AssignTo: &mw.logView, ReadOnly: true, VScroll: true, MinSize: Size{0, 200}},
			Composite{
				Layout: HBox{},
				Children: []Widget{
					HSpacer{},
					Label{Text: "开发者: 可乐虎"},
				},
			},
		},
	}.Create()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	mw.Run()
}
