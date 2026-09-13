-- Optional demo data (the task asks for "database migration and a reasonable amount of
-- demo data"). This file is NOT applied automatically - run it explicitly:
--
--   psql "$DATABASE_URL" -f scripts/sample-data.sql
--
-- It creates one inbox with a fixed token and three events covering the interesting
-- shapes: JSON, form encoded and a binary payload.

INSERT INTO inbox (owner_key, name, token, enabled, response_code)
VALUES ('default', '示例收件箱', 'demo7k9xqz2m4vb8nt6hcf3wys5d', true, 200)
ON CONFLICT (token) DO NOTHING;

INSERT INTO event (inbox_id, method, path, query, content_type, headers, body, body_text, body_size, client_ip)
SELECT id, 'POST', '/hooks/demo7k9xqz2m4vb8nt6hcf3wys5d', 'src=github',
       'application/json',
       '[{"name":"Content-Type","value":"application/json"},{"name":"X-Github-Event","value":"push"}]'::jsonb,
       decode('7b226576656e74223a2270757368222c22726566223a22726566732f68656164732f6d61696e227d', 'hex'),
       '{"event":"push","ref":"refs/heads/main"}',
       45, '203.0.113.10'
  FROM inbox WHERE token = 'demo7k9xqz2m4vb8nt6hcf3wys5d';

INSERT INTO event (inbox_id, method, path, query, content_type, headers, body, body_text, body_size, client_ip)
SELECT id, 'POST', '/hooks/demo7k9xqz2m4vb8nt6hcf3wys5d', '',
       'application/x-www-form-urlencoded',
       '[{"name":"Content-Type","value":"application/x-www-form-urlencoded"}]'::jsonb,
       convert_to('amount=42.5&currency=CNY', 'UTF8'),
       'amount=42.5&currency=CNY', 24, '203.0.113.11'
  FROM inbox WHERE token = 'demo7k9xqz2m4vb8nt6hcf3wys5d';

-- Binary payload: valid UTF-8 check fails, so body_text stays empty and the event is
-- intentionally not searchable (see README "raw body preservation").
INSERT INTO event (inbox_id, method, path, query, content_type, headers, body, body_text, body_size, client_ip)
SELECT id, 'POST', '/hooks/demo7k9xqz2m4vb8nt6hcf3wys5d', '',
       'application/octet-stream',
       '[{"name":"Content-Type","value":"application/octet-stream"},{"name":"Authorization","value":"***redacted***","sensitive":true}]'::jsonb,
       decode('00ff80fe0d0a', 'hex'), '', 6, '203.0.113.12'
  FROM inbox WHERE token = 'demo7k9xqz2m4vb8nt6hcf3wys5d';
