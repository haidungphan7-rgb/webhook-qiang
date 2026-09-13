// Minimal local receiver used by the demo.
//
// Routes:
//   /ok    -> 200 (default)
//   /fail   -> 500, to demonstrate "response received but business failure"
//   /slow   -> answers after 30s, to demonstrate the client timeout
//
// It prints what it received, which is the whole point: you can see that the replayed
// request kept the original body and content type, and that no Authorization/Cookie
// header was forwarded.
import { createServer } from 'node:http'

const PORT = Number(process.argv[2] ?? 9099)

createServer((req, res) => {
  const chunks = []

  req.on('data', (c) => chunks.push(c))
  req.on('end', () => {
    const raw = Buffer.concat(chunks)

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
  console.log(`test receiver listening on http://127.0.0.1:${PORT}  (/ok /fail /slow)`)
})
