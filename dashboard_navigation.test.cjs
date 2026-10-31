const fs=require('node:fs'),assert=require('node:assert/strict'),vm=require('node:vm');
const modes=['dmr','ysf','nxdn','p25','m17','dstar','echolink','allstar','conference'];
for(const file of ['dashboard.html','ysf-status.html']){const html=fs.readFileSync(file,'utf8');const nav=html.match(/<nav aria-label="Dashboard navigation"[\s\S]*?<\/nav>/)?.[0];assert.ok(nav,file+' has a navigation landmark');for(const slug of modes)assert.ok(nav.includes('/'+slug+'-status.html'),file+' links '+slug);assert.ok(nav.includes('/DVHub-Remote.html'));}
for(const file of ['DVHub-Remote.html','client.html'])assert.ok(fs.readFileSync(file,'utf8').includes('href="/mode-dashboards.html"'));
const shared=fs.readFileSync('mode-status.js','utf8');assert.ok(shared.includes("'Voice TX/RX','/DVHub-Remote.html'"));assert.ok(shared.includes("'All dashboards','/mode-dashboards.html'"));
for(const file of ['dashboard.html','ysf-status.html','DVHub-Remote.html','client.html'])for(const script of fs.readFileSync(file,'utf8').matchAll(/<script[^>]*>([\s\S]*?)<\/script>/g))if(script[1].trim())new vm.Script(script[1]);
for(const slug of modes)assert.ok(fs.existsSync(slug+'-status.html'));console.log('PASS: all mode and voice navigation links, destination files and inline JavaScript');
