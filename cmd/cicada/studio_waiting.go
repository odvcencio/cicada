package main

// The invalid startup page has no draft to protect. It can reload once the
// file validates, without replacing a working Studio frame on every save.
const studioWaitingPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Cicada Studio</title></head>
<body><p role="status">Score has errors. Save a valid score to open Studio.</p>
<script>
setInterval(async () => {
  try {
    const response = await fetch('/api/state', {cache:'no-store'});
    if (response.ok && (await response.json()).valid) location.reload();
  } catch {}
}, 1500);
</script></body></html>`
