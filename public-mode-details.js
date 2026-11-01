(function(){'use strict';
const modeInfo=[
 ['dmr','DMR','Local Homebrew master / TG23530',['dvhub-gateway.service'],'DMR'],
 ['ysf','YSF','YSF 23530 / UDP 42000',['ysfreflector.service','ysf2dmr.service'],'YSF'],
 ['nxdn','NXDN','NXDN 23530 / UDP 41400',['nxdnreflector.service','nxdn2dmr.service'],'NXDN'],
 ['p25','P25','P25 23530 / UDP 41000',['p25reflector.service','p252dmr.service'],'P25'],
 ['m17','M17','M17-YLH / module A / UDP 17000',['mrefd.service','usrp2m17.service'],'M17'],
 ['dstar','D-STAR / XLX','XLXYOR / module A / DVxCode',['xlxd.service','dvxbridge.service'],'D-STAR'],
 ['echolink','EchoLink','2E0LXY-L / 621907 / via AllStar',['asterisk.service','usrp2dmr.service'],'ECHOLINK'],
 ['allstar','AllStar','Node 530471 / PCM bus',['asterisk.service','usrp2dmr.service'],'ALLSTAR'],
 ['conference','TG23530 conference','Shared cross-mode conference',['dvhub-gateway.service','usrp2dmr.service','ysf2dmr.service'],'']
];
const escape=v=>String(v??'Not supplied').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
function models(data){return modeInfo.map(([id,label,identity,units,mode])=>{const route=(data.routes||[]).find(r=>r.id===id)||{};const calls=(data.activity||[]).filter(c=>id==='conference'?String(c.target||'').includes('23530'):(id==='dstar'?['D-STAR','DSTAR','XLX'].includes(String(c.mode).toUpperCase()):String(c.mode).toUpperCase()===mode));return{id,label,identity,status:route.status||'inactive',detail:route.detail||'Mode telemetry unavailable',calls,last:calls[0],services:units.map(unit=>(data.system?.services||[]).find(s=>s.unit===unit)||{label:unit,status:'inactive',detail:'Not supplied'})}})}
function flowEvidence(source,active,route,sourceMode){if(!active)return'idle';if(!sourceMode)return'unknown-source';if(route===sourceMode)return'origin';return source==='good'?'eligible':'unavailable'}
if(typeof module!=='undefined'&&module.exports){module.exports={models,escape,flowEvidence};return}
let latest={},ysf=null,ysfError='',ysfAt=0,ysfBusy=false;
const row=(name,value)=>`<div class="public-kv"><span>${escape(name)}</span><strong>${escape(value)}</strong></div>`;
function draw(){const holder=document.getElementById('public-mode-details');if(!holder)return;holder.innerHTML=models(latest).map(m=>{const status=['good','warning','fault'].includes(m.status)?m.status:'inactive';let extra='';if(m.id==='dmr'){for(const [label,key]of [['FreeSTAR','freestar'],['BrandMeister','brandmeister'],['TGIF','tgif']])extra+=row(label,latest.conference?.[key]||'Not supplied')}
if(m.id==='ysf'){if(ysf&&!ysfError){extra+=row('Connected gateways',ysf.connected_count??'Not supplied')+row('Reflector uptime',Number.isFinite(ysf.uptime_seconds)?Math.floor(ysf.uptime_seconds/3600)+' hours':'Not supplied')+row('Transmissions today',ysf.transmissions_today??'Not supplied');extra+=`<div class="public-peer-list"><strong>Connected YSF gateways</strong>${(ysf.connected_gateways||[]).length?(ysf.connected_gateways||[]).map(g=>`<p>${escape(g.callsign||'Unknown')} | ${escape(g.protocol||'YSF')} | ${escape(g.status||'Status not supplied')}${g.last_source?' | Last source: '+escape(g.last_source):''}</p>`).join(''):'<p>No gateways reported by the reflector.</p>'}</div>`}else extra+=row('YSF gateway telemetry',ysfError||'Loading...')}
const last=m.last;return `<article class="public-mode-card" id="details-${m.id}"><div class="public-mode-heading"><h3>${escape(m.label)}</h3><span class="public-mode-status ${status}">${status==='good'?'SERVICES READY':status==='fault'?'FAULT':status==='warning'?'DEGRADED':'UNKNOWN'}</span></div><p class="public-mode-identity">${escape(m.identity)}</p><p>${escape(m.detail)}</p>${m.services.map(s=>row(s.label,s.detail||s.status||'Unknown')).join('')}${extra}${row('Matching retained calls',m.calls.length)}${row('Last recorded source',last?last.callsign||last.raw_id||'Unknown':'No matching retained calls')}${row('Last recorded time (UTC)',last?last.timestamp||[last.date,last.time].filter(Boolean).join(' '):'Not supplied')}${row('Last recorded destination',last?.target||'Not supplied')}<p class="public-mode-note">${m.id==='ysf'?'Gateway details are reflector-reported.':'Connected peer lists and exact per-mode uptime are not exposed by the public API.'} Calls are filtered from the shared last-50-call history; zero does not prove no traffic.</p><a href="/${m.id==='conference'?'conference':m.id}-status.html">Dedicated ${escape(m.label)} status page</a></article>`}).join('')}
async function updateYSF(){if(ysfBusy||Date.now()-ysfAt<30000||document.hidden)return;ysfBusy=true;ysfAt=Date.now();try{const r=await fetch('/api/public/ysf_dashboard',{cache:'no-store',signal:AbortSignal.timeout(8000)});if(!r.ok)throw Error();ysf=await r.json();ysfError=''}catch{ysfError='Unavailable - previous gateway data is not shown as live'}finally{ysfBusy=false;draw()}}
window.publicFlowEvidence=flowEvidence;
window.publicModeDetailsUnavailable=()=>{latest={};ysfError='System telemetry unavailable';draw()};
window.renderPublicModeDetails=data=>{latest=data;draw();updateYSF()};
})();
