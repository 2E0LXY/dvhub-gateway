const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const html=fs.readFileSync('dashboard.html','utf8'),line=html.split(/\r?\n/).find(l=>l.includes('function vocoderQualityText('));
const format=v=>vm.runInNewContext(line+';vocoderQualityText(v)',{v});
assert.match(format({level:'idle'}),/neutral/);
assert.match(format({level:'good',samples:100,successful_samples:100,failures:0,latency_p95_ms:100.1,window_seconds:30}),/100.1 ms p95 \(successful\).*0\/100 failed/);
assert.match(format({level:'fault',samples:100,successful_samples:0,failures:100,latency_p95_ms:0}),/No successful conversions.*100\/100 failed/);
assert.ok(html.includes('ms jitter p95'));assert.ok(html.includes('Jitter:'));console.log('PASS: latency versus timeout display, neutral idle and explicit jitter labels');
