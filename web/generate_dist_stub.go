//go:build ignore

package main

import (
	"errors"
	"os"
	"path"
)

func main() {
	const distDir = "./dist"

	if _, err := os.Stat(distDir); err != nil && errors.Is(err, os.ErrNotExist) {
		if err = os.Mkdir(distDir, 0755); err != nil {
			panic(err)
		}
	}

	var (
		indexPath  = path.Join(distDir, "index.html")
		robotsPath = path.Join(distDir, "robots.txt")
	)

	// The stub is what a fresh clone ships with, so it has to explain itself: a silent
	// empty page makes the project look broken on the very first run.
	if _, err := os.Stat(indexPath); err != nil && errors.Is(err, os.ErrNotExist) {
		stub := `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>前端尚未构建</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 42rem; margin: 4rem auto; padding: 0 1.5rem; line-height: 1.7; color: #1f2328; }
code, pre { background: #f1f3f5; border-radius: 4px; }
code { padding: .15rem .35rem; }
pre { padding: .8rem 1rem; overflow-x: auto; }
</style>
</head>
<body>
<h1>前端尚未构建</h1>
<p>这是一个占位页（<code>web/dist/index.html</code>），说明前端资源还没有打包。</p>
<p>在项目根目录执行下面任意一条，然后重启服务：</p>
<pre>npm --prefix ./web ci
npm --prefix ./web run build

# 或者一条命令（自动构建前端并启动）
./dev.ps1</pre>
<p>后端不受影响：<code>/api/v1/...</code> 与 <code>/hooks/&lt;token&gt;</code> 现在就可以用。</p>
</body>
</html>
`

		if err = os.WriteFile(indexPath, []byte(stub), 0644); err != nil {
			panic(err)
		}
	}

	if _, err := os.Stat(robotsPath); err != nil && errors.Is(err, os.ErrNotExist) {
		if err = os.WriteFile(robotsPath, []byte("User-agent: *\nDisallow: /\n"), 0644); err != nil {
			panic(err)
		}
	}
}
