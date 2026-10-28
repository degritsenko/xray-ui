'use strict';
const $=id=>document.getElementById(id);
let state=null,csrf='',busy=false,checking=false,probeController=null,authGeneration=0;
async function api(path,options={}){
 const method=options.method||'GET',generation=authGeneration;
 const response=await fetch('/api'+path,{method,credentials:'same-origin',headers:{'Content-Type':'application/json',...(method!=='GET'?{'X-CSRF-Token':csrf}:{})},...(options.body!==undefined?{body:JSON.stringify(options.body)}:{}),...(options.signal?{signal:options.signal}:{})});
 let data={};try{data=await response.json()}catch{}
 if(!response.ok){if(response.status===401&&path!=='/login'&&generation===authGeneration)showLogin();throw new Error(data.error||'Request failed')}
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
 try{await work();await load();notice(message)}catch(error){notice(error.message,true);await load()}
 finally{setBusy(false)}
}
function refresh(sub){return action(()=>api(`/subscriptions/${sub.id}/refresh`,{method:'POST',body:{}}),'Subscription updated','Refreshing subscription…')}
function render(){
 if(!state)return;
 const active=state.active,connected=state.running,status=$('connection-status');
 const version=state.coreVersion?` ${state.coreVersion}`:'';
 status.textContent=connected?`Xray${version} is running`:active?(active.enabled?'Xray is not running':'Stopped'):'Not connected';
 status.className='status'+(connected?' connected':active?.enabled?' failed':'');
 $('active-name').textContent=active?.node.name||'Select a server';
 $('active-detail').textContent=active?`${active.node.server}:${active.node.port}`:'No server selected.';
 $('source-warning').hidden=!active?.sourceChanged;
 $('core-toggle').textContent=connected?'Stop':'Start';$('core-toggle').disabled=busy||!active;$('core-restart').disabled=busy||!active||!connected;
 const subscriptions=state.subscriptions,multiple=subscriptions.length>1;
 $('subscription-meta').textContent=multiple?`${subscriptions.length} subscriptions`:subscriptions.length?`${subscriptions[0].name} · ${updatedAt(subscriptions[0])}`:'';
 $('refresh-source').hidden=multiple;$('refresh-source').disabled=busy||!subscriptions.length;
 $('probe-servers').disabled=busy||checking||state.probing||!subscriptions.some(s=>s.nodes.length);
 $('probe-servers').textContent=(checking||state.probing)?'Checking…':multiple?'Check all servers':'Check servers';
 const hasNodes=subscriptions.some(s=>s.nodes.length);
 $('server-search').hidden=!hasNodes;$('switch-hint').hidden=!hasNodes;
 const rows=$('server-list');rows.replaceChildren();const query=$('server-search').value.trim().toLocaleLowerCase('en');
 let visible=0;
 subscriptions.forEach((sub,index)=>{
  const nodes=sub.nodes.filter(n=>`${n.name} ${n.server}`.toLocaleLowerCase('en').includes(query));
  if(query&&!nodes.length)return;
  const name=sub.name||`Subscription ${index+1}`;
  const group=element('section','','subscription-group');group.setAttribute('aria-label',name);
  if(multiple){const heading=element('div','','subscription-heading'),info=element('div','');info.append(element('span',name,'subscription-title'),element('span',updatedAt(sub),'hint'));const button=element('button','Refresh','secondary');button.type='button';button.disabled=busy;button.setAttribute('aria-label',`Refresh ${name}`);button.addEventListener('click',()=>refresh(sub));heading.append(info,button);group.append(heading)}
  if(sub.error){const error=element('p',sub.error,'source-error');error.setAttribute('role','alert');group.append(error)}
  const list=element('div','','node-list');
  nodes.forEach(node=>{
   visible++;
   const chosen=active?.subscriptionID===sub.id&&active.node.id===node.id&&!active.sourceChanged;
   const row=element('div','',`server-row${chosen?' active':''}`),info=element('div','','server-info');
   info.append(element('div',node.name,'server-name'),element('div',`${node.server}:${node.port}`,'server-address'));row.append(info);
   const result=node.probe,delay=element('span',result?(result.reachable?`${result.milliseconds} ms`:'Unavailable'):'Not checked',`server-delay${result&&!result.reachable?' unavailable':!result?' unknown':''}`);delay.title='HTTPS request latency through this VPN server';row.append(delay);
   if(chosen&&connected)row.append(element('span','● Connected','selected-badge'));
   else{const button=element('button','Connect','secondary');button.type='button';button.disabled=busy;button.addEventListener('click',()=>action(()=>api('/activate',{method:'POST',body:{subscriptionID:sub.id,nodeID:node.id}}),'Server connected','Connecting…'));row.append(button)}
   list.append(row);
  });
  if(!nodes.length)list.append(element('p','Refresh the subscription to load servers.','empty'));
  group.append(list);rows.append(group);
 });
 if(!visible&&query)rows.append(element('p','No servers found.','empty'));
 if(!subscriptions.length)rows.append(element('p','Set SUBSCRIPTION_URL in the container environment and restart the container.','empty'));
 $('logs').textContent=state.logs.map(entry=>`${new Date(entry.time).toLocaleTimeString('en-GB')}  ${entry.message}`).join('\n')||'No events yet.';
 $('version').textContent=`xray-ui · ${state.version}`;
 if(state.lastError&&!$('notice').textContent)notice(state.lastError,true);
}
$('login-form').addEventListener('submit',async event=>{event.preventDefault();$('login-button').disabled=true;$('login-error').hidden=true;
 try{const result=await api('/login',{method:'POST',body:{password:$('password').value}});authGeneration++;csrf=result.csrf;$('password').value='';await load()}
 catch(error){$('login-error').textContent=error.message;$('login-error').hidden=false}
 finally{$('login-button').disabled=false}
});
$('logout').addEventListener('click',async()=>{try{await api('/logout',{method:'POST',body:{}});showLogin()}catch(error){notice(error.message,true)}});
$('core-toggle').addEventListener('click',()=>{const running=state?.running;action(()=>api(`/xray/${running?'stop':'start'}`,{method:'POST',body:{}}),running?'Xray stopped':'Xray started',running?'Stopping Xray…':'Starting Xray…')});
$('core-restart').addEventListener('click',()=>action(()=>api('/xray/restart',{method:'POST',body:{}}),'Xray restarted','Restarting Xray…'));
$('refresh-source').addEventListener('click',()=>{const sub=state?.subscriptions[0];if(sub)refresh(sub)});
async function checkServers(){
 if(busy||checking||state?.probing)return;
 const controller=new AbortController();probeController=controller;checking=true;render();notice('Checking servers through VPN…');
 try{
  await api('/probe',{method:'POST',body:{},signal:controller.signal});
  if(probeController===controller&&state){await load();if(probeController===controller&&state)notice('Server check complete')}
 }catch(error){if(error.name!=='AbortError'&&probeController===controller&&state)notice(error.message,true)}
 finally{if(probeController===controller){probeController=null;checking=false;if(state)render()}}
}
$('probe-servers').addEventListener('click',checkServers);
$('server-search').addEventListener('input',render);
load();setInterval(()=>{if(!$('app').hidden&&!document.hidden)load()},5000);
