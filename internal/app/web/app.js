'use strict';
const $=id=>document.getElementById(id);
let state=null,csrf='',busy=false,checking=false,probeController=null,authGeneration=0;
async function api(path,options={}){
 const method=options.method||'GET',generation=authGeneration;
 const response=await fetch('/api'+path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...(method!=='GET'?{'X-CSRF-Token':csrf}:{})},...(options.body!==undefined?{body:JSON.stringify(options.body)}:{}),...(options.signal?{signal:options.signal}:{})});
 let data={};try{data=await response.json()}catch{}
 if(!response.ok){if(response.status===401&&path!=='/login'&&generation===authGeneration)showLogin();const error=new Error(data.error||'Request failed');error.scope=data.scope;throw error}
 return data;
}
function showLogin(){authGeneration++;probeController?.abort();probeController=null;checking=false;$('app').hidden=true;$('login-screen').hidden=false;state=null;csrf='';notice('')}
function notice(message,error=false){$('notice').textContent=message;$('notice').classList.toggle('error',error);$('notice').hidden=!message}
async function load(){
 const generation=authGeneration;
 try{const next=await api('/state');if(generation!==authGeneration)return false;state=next;csrf=state.csrf;$('login-screen').hidden=true;$('app').hidden=false;render();return true}
 catch(error){if(generation===authGeneration&&state)notice(error.message,true);return false}
}
function element(tag,text,className=''){const e=document.createElement(tag);e.textContent=text;if(className)e.className=className;return e}
function updatedAt(sub){return sub.updatedAt&&!sub.updatedAt.startsWith('0001-')?`Updated ${new Date(sub.updatedAt).toLocaleString('en-GB',{day:'numeric',month:'short',hour:'2-digit',minute:'2-digit'})}`:'Not updated yet'}
function setBusy(value,text='Applying settings…'){
 busy=value;$('busy-bar').hidden=!value;$('busy-text').textContent=text;
 document.querySelectorAll('#app button').forEach(b=>b.disabled=value);if(!value&&state)render();
}
async function action(work,message,text){
 if(busy)return;setBusy(true,text);notice('');
 try{await work();await load();notice(message)}catch(error){if(error.scope==='subscription')notice('');else notice(error.message,true);await load()}
 finally{setBusy(false)}
}
function refresh(sub){return action(()=>api(`/subscriptions/${sub.id}/refresh`,{method:'POST',body:{}}),'Subscription updated','Refreshing subscription…')}
let preferences={favorites:[],collapsed:[],filter:'all',sort:'subscription'};
try{const saved=JSON.parse(localStorage.getItem('xray-ui-preferences')||'{}');for(const key of ['favorites','collapsed'])if(Array.isArray(saved[key]))preferences[key]=saved[key].filter(v=>typeof v==='string'&&v.length<512).slice(0,1024);if(saved.filter==='favorites')preferences.filter='favorites';if(saved.sort==='latency')preferences.sort='latency'}catch{}
let autoCheckUpdating=false,checkScope={};
const nodeKey=(sub,node)=>`${sub.preferenceID||sub.id}/${node.id}`;
const groupKey=sub=>sub.preferenceID||sub.id;
function savePreferences(){try{localStorage.setItem('xray-ui-preferences',JSON.stringify(preferences))}catch{}}
function elapsed(value){const date=Date.parse(value);return Number.isFinite(date)&&date>0?Math.max(0,(Date.now()-date)/1000):null}
function age(value){const seconds=elapsed(value);if(seconds===null)return 'Not checked';if(seconds<30)return 'just now';if(seconds<60)return `${Math.floor(seconds)} sec ago`;if(seconds<3600)return `${Math.floor(seconds/60)} min ago`;if(seconds<86400)return `${Math.floor(seconds/3600)} hr ago`;return `${Math.floor(seconds/86400)} days ago`}
function stale(result,active=false){const seconds=elapsed(result?.checkedAt);return seconds!==null&&seconds>(active?120:(state?.probeIntervalSeconds||600))}
function latencyText(result){return !result?'Not checked':result.reachable?`${result.milliseconds} ms`:'Check failed'}
function togglePreference(key,value){const list=preferences[key],index=list.indexOf(value);if(index<0){if(list.length<1024)list.push(value)}else list.splice(index,1);savePreferences();render()}
function formatBytes(bytes){
 const units=['B','KB','MB','GB','TB','PB'];let value=bytes,index=0;
 while(value>=1000&&index<units.length-1){value/=1000;index++}
 return `${new Intl.NumberFormat('en-GB',{maximumFractionDigits:index?1:0}).format(value)} ${units[index]}`;
}
function appendSubscriptionUsage(group,sub){
 const usage=sub.usage;if(!usage)return;
 const valid=value=>Number.isSafeInteger(value)&&value>=0;
 const knownTraffic=valid(usage.upload)||valid(usage.download),knownTotal=valid(usage.total),knownExpiry=valid(usage.expire);
 if(!knownTraffic&&!knownTotal&&!knownExpiry)return;
 const used=(valid(usage.upload)?usage.upload:0)+(valid(usage.download)?usage.download:0);
 const box=element('div','','subscription-usage'),summary=element('div','','usage-summary');
 let traffic='';
 if(knownTraffic)traffic=knownTotal&&usage.total>0?`${formatBytes(used)} / ${formatBytes(usage.total)}`:`${formatBytes(used)} used${knownTotal?' · Unlimited':''}`;
 else if(knownTotal)traffic=usage.total===0?'Unlimited traffic':`Traffic limit ${formatBytes(usage.total)}`;
 if(traffic){const label=element('span',traffic,'usage-traffic');label.title='Traffic reported by the provider; updated when the subscription is refreshed';summary.append(label)}
 if(knownExpiry){
  let text='No expiry',className='usage-expiry';
  if(usage.expire>0){const expired=usage.expire*1000<=Date.now();text=`${expired?'Expired':'Expires'} ${new Date(usage.expire*1000).toLocaleDateString('en-GB',{day:'2-digit',month:'short',year:'numeric',timeZone:'UTC'})}`;if(expired)className+=' expired'}
  const label=element('span',text,className);if(usage.expire>0)label.title=new Date(usage.expire*1000).toISOString();summary.append(label);
 }
 box.append(summary);
 if(knownTraffic&&knownTotal&&usage.total>0){
  const percent=Math.min(100,Math.max(0,used/usage.total*100));
  const track=element('div','',`usage-track${used>=usage.total?' exhausted':''}`);track.setAttribute('role','progressbar');track.setAttribute('aria-label',`Traffic quota used for ${sub.name}`);track.setAttribute('aria-valuemin','0');track.setAttribute('aria-valuemax','100');track.setAttribute('aria-valuenow',String(Math.round(percent)));track.setAttribute('aria-valuetext',traffic);
  const bar=element('div','','usage-fill');bar.style.width=percent+'%';track.append(bar);box.append(track);
 }
 group.append(box);
}
function render(){
 if(!state)return;
 const active=state.active,connected=state.running,status=$('connection-status');
 const version=state.coreVersion?` ${state.coreVersion}`:'';
 status.textContent=connected?`Xray${version} is running`:active?(active.enabled?'Xray is not running':'Stopped'):'Not connected';
 status.className='status'+(connected?' connected':active?.enabled?' failed':'');
 $('active-name').textContent=active?.node.name||'Select a server';
 const activeSource=state.subscriptions.find(s=>s.id===active?.subscriptionID);
 $('active-detail').textContent=active?`${active.node.server}:${active.node.port}${activeSource?' · '+activeSource.name:''}`:'No server selected.';
 const activeResult=active?.node.probe||activeSource?.nodes.find(n=>n.id===active?.node.id)?.probe;
 $('active-latency').textContent=activeResult?.reachable?String(activeResult.milliseconds):'—';
 $('active-checked').textContent=activeResult?(activeResult.reachable?'Checked ':'Check failed · ')+age(activeResult.checkedAt):'Not checked';
 $('active-delay').className='active-delay'+(stale(activeResult,true)?' stale':'');
 $('source-warning').hidden=!active?.sourceChanged;
 $('core-toggle').textContent=connected?'Stop':'Start';$('core-toggle').disabled=busy||!active;$('core-restart').disabled=busy||!active||!connected;
 const subscriptions=state.subscriptions,hasNodes=subscriptions.some(s=>s.nodes.length),probing=checking||state.probing;
 $('subscription-meta').textContent=`${subscriptions.length} ${subscriptions.length===1?'subscription':'subscriptions'} · ${subscriptions.reduce((n,s)=>n+s.nodes.length,0)} servers`;
 $('probe-servers').disabled=busy||probing||!hasNodes;$('probe-servers').textContent=probing?'Checking…':'Check all';
 $('server-search').hidden=!hasNodes;$('switch-hint').hidden=!hasNodes;
 $('auto-check-interval').disabled=autoCheckUpdating;
 if(!autoCheckUpdating)$('auto-check-interval').value=String(state.probeIntervalSeconds??600);
 $('server-sort').value=preferences.sort;
 $('filter-all').setAttribute('aria-pressed',String(preferences.filter==='all'));
 $('filter-favorites').setAttribute('aria-pressed',String(preferences.filter==='favorites'));
 const remaining=Date.parse(state.nextProbeAt)-Date.now();
 $('next-check').textContent=state.probeIntervalSeconds===0?'Automatic checks off':probing?'Checking servers…':`Next check in ${Math.max(1,Math.ceil(remaining/60000))||1} min · Active server about every 60 sec`;
 const focusedKey=document.activeElement?.getAttribute?.('data-focus');
 const rows=$('server-list');rows.replaceChildren();const query=$('server-search').value.trim().toLocaleLowerCase('en');
 let visible=0;
 subscriptions.forEach((sub,index)=>{
  let nodes=sub.nodes.filter(n=>`${n.name} ${n.server}`.toLocaleLowerCase('en').includes(query)&&(preferences.filter!=='favorites'||preferences.favorites.includes(nodeKey(sub,n))));
  if((query||preferences.filter==='favorites')&&!nodes.length)return;
  if(preferences.sort==='latency')nodes=[...nodes].sort((a,b)=>(a.probe?.reachable?a.probe.milliseconds:Infinity)-(b.probe?.reachable?b.probe.milliseconds:Infinity));
  visible+=nodes.length;
  const name=sub.name||`Subscription ${index+1}`,collapsed=preferences.collapsed.includes(groupKey(sub));
  const group=element('section','','subscription-group');group.setAttribute('aria-label',name);
  const heading=element('div','','subscription-heading'),info=element('div','','subscription-info');
  const title=element('button',`${collapsed?'›':'⌄'} ${name}`,'subscription-title');title.type='button';title.setAttribute('data-focus','group:'+groupKey(sub));title.setAttribute('aria-expanded',String(!collapsed));title.addEventListener('click',()=>togglePreference('collapsed',groupKey(sub)));
  info.append(title,element('span',String(sub.nodes.length),'node-count'),element('span',updatedAt(sub),'hint'));
  const actions=element('div','','source-actions');
  const refreshButton=element('button','↻ Refresh subscription','quiet');refreshButton.type='button';refreshButton.disabled=busy;refreshButton.setAttribute('aria-label',`Refresh ${name}`);refreshButton.addEventListener('click',()=>refresh(sub));
  const check=element('button','Check','quiet');check.type='button';check.disabled=busy||probing||!sub.nodes.length;check.setAttribute('aria-label',`Check ${name}`);check.addEventListener('click',()=>checkServers({subscriptionID:sub.id}));
  actions.append(refreshButton,check);heading.append(info,actions);group.append(heading);
  appendSubscriptionUsage(group,sub);
  if(sub.error){const error=element('p',sub.error,'source-error');error.setAttribute('role','alert');group.append(error)}
  if(!collapsed){
   const list=element('div','','node-list');
   nodes.forEach(node=>{
    const chosen=active?.subscriptionID===sub.id&&active.node.id===node.id&&!active.sourceChanged;
    const row=element('div','',`server-row${chosen?' active':''}`),info=element('div','','server-info');
    const favorite=preferences.favorites.includes(nodeKey(sub,node));
    const star=element('button',favorite?'★':'☆',`favorite${favorite?' selected':''}`);star.type='button';star.setAttribute('data-focus','favorite:'+nodeKey(sub,node));star.disabled=busy;star.setAttribute('aria-label',`${favorite?'Remove':'Add'} ${node.name} ${favorite?'from':'to'} favorites`);star.setAttribute('aria-pressed',String(favorite));star.addEventListener('click',()=>togglePreference('favorites',nodeKey(sub,node)));row.append(star);
    info.append(element('div',node.name,'server-name'),element('div',`${node.server}:${node.port}`,'server-address'));row.append(info);
    const result=node.probe,pending=state.probing&&state.probingNode===sub.id+'/'+node.id||checking&&checkScope.nodeID===node.id&&checkScope.subscriptionID===sub.id;
    const delay=element('button','',`server-delay${result&&!result.reachable?' unavailable':!result?' unknown':result.milliseconds>150?' slow':''}${stale(result,chosen&&connected)?' stale':''}`);delay.type='button';delay.setAttribute('data-focus','probe:'+nodeKey(sub,node));delay.disabled=busy||probing;delay.title='Check HTTPS latency through this VPN server';delay.setAttribute('aria-label',`Check ${node.name}`);
    delay.append(element('span',latencyText(result),'delay-value'),element('span',pending?'Checking…':result?age(result.checkedAt):'Click to check','checked-age'));delay.addEventListener('click',()=>checkServers({subscriptionID:sub.id,nodeID:node.id}));row.append(delay);
    if(chosen&&connected)row.append(element('span','● Connected','selected-badge'));
    else{const button=element('button','Connect','secondary');button.type='button';button.setAttribute('data-focus','connect:'+nodeKey(sub,node));button.disabled=busy;button.addEventListener('click',()=>action(()=>api('/activate',{method:'POST',body:{subscriptionID:sub.id,nodeID:node.id}}),'Server connected','Connecting…'));row.append(button)}
    list.append(row);
   });
   if(!nodes.length&&!sub.error)list.append(element('p','Refresh the subscription to load servers.','empty'));
   if(nodes.length||!sub.error)group.append(list);
  }
  rows.append(group);
 });
 if(!visible&&(query||preferences.filter==='favorites'))rows.append(element('p',preferences.filter==='favorites'?'No favorite servers match.':'No servers found.','empty'));
 if(!subscriptions.length)rows.append(element('p','Set SUBSCRIPTION_URL in the container environment and restart the container.','empty'));
 if(focusedKey){const target=Array.from(rows.querySelectorAll?.('[data-focus]')||[]).find(e=>e.getAttribute('data-focus')===focusedKey);if(target&&!target.disabled)target.focus({preventScroll:true})}
 $('logs').textContent=state.logs.map(entry=>`${new Date(entry.time).toLocaleTimeString('en-GB')}  ${entry.message}`).join('\n')||'No events yet.';
 $('version').textContent=`xray-ui · ${state.version}`;
 if(state.lastError&&!$('notice').textContent)notice(state.lastError,true);
}
$('logout').addEventListener('click',async()=>{try{await api('/logout',{method:'POST',body:{}});showLogin()}catch(error){notice(error.message,true)}});
$('core-toggle').addEventListener('click',()=>{const running=state?.running;action(()=>api(`/xray/${running?'stop':'start'}`,{method:'POST',body:{}}),running?'Xray stopped':'Xray started',running?'Stopping Xray…':'Starting Xray…')});
$('core-restart').addEventListener('click',()=>action(()=>api('/xray/restart',{method:'POST',body:{}}),'Xray restarted','Restarting Xray…'));
async function checkServers(scope={}){
 if(busy||checking||state?.probing)return;
 const controller=new AbortController();probeController=controller;checking=true;checkScope=scope;render();notice('Checking servers through VPN…');
 try{
  await api('/probe',{method:'POST',body:scope,signal:controller.signal});
  if(probeController===controller&&state){await load();if(probeController===controller&&state)notice('Server check complete')}
 }catch(error){if(error.name!=='AbortError'&&probeController===controller&&state)notice(error.message,true)}
 finally{if(probeController===controller){probeController=null;checking=false;if(state)render()}}
}
$('probe-servers').addEventListener('click',()=>checkServers());
$('server-search').addEventListener('input',render);
$('server-sort').addEventListener('change',()=>{preferences.sort=$('server-sort').value;savePreferences();render()});
$('filter-all').addEventListener('click',()=>{preferences.filter='all';savePreferences();render()});
$('filter-favorites').addEventListener('click',()=>{preferences.filter='favorites';savePreferences();render()});
$('auto-check-interval').addEventListener('change',async()=>{
 const generation=authGeneration;autoCheckUpdating=true;$('auto-check-interval').disabled=true;
 try{await api('/probe/settings',{method:'POST',body:{intervalSeconds:Number($('auto-check-interval').value)}});if(generation===authGeneration)notice('Automatic check interval updated')}
 catch(error){if(generation===authGeneration)notice(error.message,true)}
 finally{autoCheckUpdating=false;if(generation===authGeneration)await load()}
});
load();setInterval(()=>{if(!$('app').hidden&&!document.hidden)load()},5000);
