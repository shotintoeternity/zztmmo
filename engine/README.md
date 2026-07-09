# zztgo

`zztgo` is a (not exactly finished) port of Adrian Siekierka’s source code [reconstruction of ZZT](https://github.com/asiekierka/reconstruction-of-zzt/) to Go. I created it using a [Pascal-to-Go converter](https://github.com/benhoyt/pas2go) that I wrote, as well as the [tcell](https://github.com/gdamore/tcell) terminal library for graphics.

To run the terminal game locally: install Go, clone the repo, and run `go run ./cmd/zztgo`. To smoke-test the headless engine without opening a terminal UI, run `go run ./cmd/zzt-smoke`. To start the JSON WebSocket server, run `go run ./cmd/zzt-server`. To run the browser client locally, start the server and then run `npm install --prefix web` followed by `npm --prefix web run dev`; Vite proxies `/ws` to the Go server. For a single served app, run `npm --prefix web run build` and then `go run ./cmd/zzt-server`. If you want the terminal game to look a bit more authentic, install an [IBM EGA font](https://int10h.org/oldschool-pc-fonts/fontlist/#ibmega) and adjust the line spacing to zero. On macOS you can use [this Terminal settings file](https://github.com/benhoyt/zztgo/blob/master/zzt.terminal).

[**Read the full story here**.](https://benhoyt.com/writings/zzt-in-go/)
