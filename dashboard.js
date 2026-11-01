const $ = id => document.getElementById(id);
let busy = false;
function text(id,value) { $(id).textContent = value; }
async function get(path) {
 const r = await fetch(path,{cache:'no-store',signal:AbortSignal.timeout(85000)});
 if (!r.ok) throw new Error(r.status===401?'Sign in again to view tasks.':r.status===503?'Temporarily unavailable or refreshing. Retrying…':'Task data unavailable.');
 return r.json();
}
function prURL(value) {
 try {const u=new URL(value);return u.protocol==='https:'&&u.hostname==='github.com'&&/^\/[^/]+\/[^/]+\/pull\/\d+$/.test(u.pathname)&&!u.username&&!u.password?u.href:null;}catch{return null;}
}
function eventView(e) {
 const item=e.item||e;
 if (item.type==='reasoning' || e.type==='thinking') return null;
 let title=e.type||e.event||'Agent event',body='';
 if (item.command) title=item.command+(item.exit_code!==undefined?' · exit '+item.exit_code:'');
 if (typeof item.aggregated_output==='string') body=item.aggregated_output;
 else if (typeof item.text==='string') body=item.text;
 else if (Array.isArray(e.message?.content)) {
  body=e.message.content.filter(x=>x.type==='text').map(x=>x.text).join('\n');
  if(body)title=e.type==='assistant'?'Agent message':title;
 }
 return {title,body:body||JSON.stringify(e,null,2)};
}
async function refresh() {
 if(busy || document.hidden)return;
 busy=true;
 try {
 const tasks=await get('/api/tasks');
 $('tasks').replaceChildren();
 for(const t of tasks) {const a=document.createElement('a');a.href='#'+encodeURIComponent(t.id);a.textContent=t.id+' · '+t.phase;if(decodeURIComponent(location.hash.slice(1))===t.id)a.setAttribute('aria-current','page');$('tasks').append(a);}
 const id=decodeURIComponent(location.hash.slice(1));
 $('detail').hidden=!id;
 if(!id){text('connection',tasks.length?'Choose a task to inspect.':'No tasks yet.');return;}
 const v=await get('/api/task?id='+encodeURIComponent(id));
 if(decodeURIComponent(location.hash.slice(1))!==id)return;
 text('title',v.task.id);
 text('meta',[v.task.provider,'VM: '+(v.vm||'unknown'),'Agent: '+(v.status||'unknown'),'Controller: '+v.task.phase,v.exitCode!==null?'Turn exit: '+v.exitCode:'',v.task.branch].filter(Boolean).join(' · '));
 text('updated','Observed '+new Date(v.at).toLocaleString());
 text('notes',v.notes.join('\n'));
 const url=prURL(v.task.pr);$('pr').hidden=!url;if(url)$('pr').href=url;else $('pr').removeAttribute('href');
 text('omitted',v.eventsOmitted?v.eventsOmitted+' earlier events omitted. Showing the latest 150.':'');
 const events=$('events');events.replaceChildren();
 for(const e of v.events){const view=eventView(e);if(!view)continue;const d=document.createElement('details'),s=document.createElement('summary'),p=document.createElement('pre');s.textContent=view.title;p.textContent=view.body;d.append(s,p);events.append(d);}
 if(!v.events.length)events.textContent='No live events available.';
 text('git',(v.git||'No live diff available or no changes.')+(v.gitTruncated?'\n[Display truncated at 180 KB]':''));
 text('connection','Connected · read only');
 } catch(e) {text('connection',e.message+' Previously displayed data may be stale.');}
 finally {busy=false;}
}
window.addEventListener('hashchange',()=>{$('detail').hidden=true;refresh();});
document.addEventListener('visibilitychange',refresh);
setInterval(refresh,15000);refresh();
