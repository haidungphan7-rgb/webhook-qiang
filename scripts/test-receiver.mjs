// Minimal local receiver used by the acceptance scripts and the demo.
//
// Modes (second CLI argument):
//   normal  (default) - routes:
//     /ok    -> 200 (default)
//     /fail  -> 500, to demonstrate "response received but business failure"
//     /slow  -> answers after 30s, to demonstrate the client timeout
//   partial - promises Content-Length: 100, sends 7 bytes ("partial") and destroys the
//     socket: a 2xx whose body can never be read. The 7 bytes are chosen so the kept
//     preview is the literal string "partial".
//   big     - answers 5000 bytes on every path, more than the default replay_max_preview
//     (4096), so the preview must be truncated.
//
// It prints what it received, which is the whole point: you can see that the replayed
// request kept the original body and content type, and that no Authorization/Cookie
// header was forwarded.
import { createServer } from 'node:http'

const PORT = Number(process.argv[2] ?? 9099)
const MODE = process.argv[3] ?? 'normal'

function log(req, raw) {
  console.log(`\n[${new Date().toISOString()}] ${req.method} ${req.url} (${MODE})`)
  console.log(`  content-type: ${req.headers['content-type'] ?? '-'}`)
  console.log(`  size: ${raw.length}`)
  console.log(`  body: ${raw.toString('utf8').slice(0, 500)}`)
}

createServer((req, res) => {
  const chunks = []

  req.on('data', (c) => chunks.push(c))
  req.on('end', () => {
    const raw = Buffer.concat(chunks)

    if (MODE === 'partial') {
      log(req, raw)
      // Headers go out with the first write; the destroy afterwards cuts the body at
      // 7 of the promised 100 bytes.
      res.writeHead(200, { 'content-type': 'text/plain', 'content-length': 100 })
      res.write('partial', () => res.destroy())
      return
    }

    if (MODE === 'big') {
      log(req, raw)
      res.writeHead(200, { 'content-type': 'text/plain' })
      res.end('B'.repeat(5000))
      return
    }

    console.log(`\n[${new Date().toISOString()}] ${req.method} ${req.url}`)
    console.log(`  content-type: ${req.headers['content-type'] ?? '-'}`)
    console.log(`  size: ${raw.length}`)
    console.log(`  headers: ${JSON.stringify(req.headers)}`)
    console.log(`  body: ${raw.toString('utf8').slice(0, 500)}`)

    if (req.url?.startsWith('/slow')) {
      setTimeout(() => {
        res.writeHead(200, { 'content-type': 'text/plain' })
        res.end('slow ok')
      }, 30_000)

      return
    }

    if (req.url?.startsWith('/fail')) {
      res.writeHead(500, { 'content-type': 'text/plain' })
      res.end('boom')

      return
    }

    res.writeHead(200, { 'content-type': 'application/json' })
    res.end(JSON.stringify({ received: true, size: raw.length }))
  })
}).listen(PORT, () => {
  console.log(`test receiver listening on http://127.0.0.1:${PORT} mode=${MODE}  (normal: /ok /fail /slow)`)
})
