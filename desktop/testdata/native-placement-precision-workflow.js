// Actual WKWebView, real guarded forms; DOM pointer/keyboard input is synthetic.
// Null audio and input off. No pointer-capture shim, hardware input or output.
(() => {
 if(location.protocol!=='http:'||!['127.0.0.1','localhost','[::1]'].includes(location.hostname)||window.__precisionSmoke)return;window.__precisionSmoke=true;
 const checks=[],errors=[],posts=[];const pause=(ms=40)=>new Promise(r=>setTimeout(r,ms));
 const node=s=>{const n=document.querySelector(s);if(!n)throw Error('Missing '+s);return n},block=(id='verse')=>node('[data-placement-block="'+id+'"]');
 async function wait(fn,name){const end=performance.now()+15000;while(performance.now()<end){if(await fn())return;await pause()}throw Error('Timed out '+name)}
 async function state(path='state'){const r=await fetch('/api/'+path,{cache:'no-store'});if(!r.ok)throw Error(path+': '+r.status);return r.json()}
 async function ready(){await wait(async()=>window.__gosx?.ready&&document.documentElement.getAttribute('data-gosx-navigation-state')==='idle'&&document.querySelector('#studio-shell[data-shell-actions=ready]')&&document.querySelector('[data-shell-placement-editor=ready]')&&!document.querySelector('form[data-gosx-form-state=pending]')&&node('#undo-form input[name=revision]').value===(await state()).revision,'fresh shell revision')}
 function change(selector,value){const n=node(selector);if(n.type==='checkbox')n.checked=value;else n.value=value;n.dispatchEvent(new Event('input',{bubbles:true}));n.dispatchEvent(new Event('change',{bubbles:true}))}
 function key(key,code,target,extra={}){target.focus();target.dispatchEvent(new KeyboardEvent('keydown',{key,code,bubbles:true,cancelable:true,...extra}))}
 const fetchOriginal=fetch;window.fetch=function(...args){const i=args[0],m=(args[1]?.method||i?.method||'GET').toUpperCase(),u=new URL(typeof i==='string'?i:i.url,location.href);if(m==='POST'&&u.pathname==='/__actions/arrange')posts.push({body:String(args[1]?.body||'')});return fetchOriginal.apply(this,args)};
 addEventListener('error',e=>errors.push(e.message||String(e.error)));addEventListener('unhandledrejection',e=>errors.push(String(e.reason)));
 const fixture=`cicada 2
 title "Native placement precision"
 tempo 96
 key c minor
 instrument tone { voice mono { out = sine(pitch) * env(gate,100ms) * velocity * 0.1 } }
 track bass tone { level = -24dB }
 track lead acid { level = -24dB }
 pattern pulse notes steps=4 { c3 . g3 . }
 pattern reply acid steps=4 { 1 . 5 . }
 arrange {
 marker end { at = @9.1.1 }
 place verse bass pulse { at = @2.1.1 length = 1bars }
 place answer lead reply { at = @5.1.1 length = 1bars }
 }
`;
 let baseline,revision;
 async function undo(){node('#undo-form button').click();await wait(async()=>(await state()).source===baseline,'one exact Undo');await ready();if((await state()).revision!==revision)throw Error('Undo revision differs')}
 async function value(attr,n,id='verse'){await wait(()=>document.querySelector('[data-placement-block="'+id+'"]')?.getAttribute('data-placement-'+attr)===String(n),id+' '+attr);await ready()}
 function pointer(type,control,x,y,extra={}){control.dispatchEvent(new PointerEvent(type,{pointerId:1,pointerType:'mouse',isPrimary:true,button:0,buttons:type==='pointerup'?0:1,clientX:x,clientY:y,bubbles:true,cancelable:true,...extra}))}
 function held({dx=96,alt=false,resize=false}={}){node('[data-placement-view=selected]').click();const control=block().querySelector(resize?'[data-placement-resize]':'.placement-select');control.scrollIntoView({block:'nearest',inline:'nearest'});const b=control.getBoundingClientRect(),x=b.left+(resize?b.width/2:20),y=b.top+b.height/2;pointer('pointerdown',control,x,y,{altKey:alt});pointer('pointermove',block(),x+dx,y,{altKey:alt});return()=>pointer('pointerup',block(),x+dx,y,{altKey:alt})}
 (async()=>{
  await ready();if(!(await state()).source.includes('title "Desktop CI"'))throw Error('Disposable native fixture required');const audio=await state('audio/config');if(String(audio.inventory.backendName||audio.inventory.backend).toLowerCase()!=='null'||audio.options.inputEnabled)throw Error('Null audio/input-off required');
  const editor=node('#score textarea');editor.focus();editor.value=fixture;editor.dispatchEvent(new Event('input',{bubbles:true}));key('s','KeyS',editor,{metaKey:true});await wait(async()=>(await state()).source.replace(/\r\n?/g,'\n')===fixture,'fixture save');await ready();
  node('#view-tab-arrange').click();await wait(()=>!node('#view-panel-arrange').hidden&&document.querySelector('[data-placement-navigation-ready]'),'Arrange ready');block().querySelector('.placement-select').click();await ready();await wait(()=>document.querySelector('[data-shell-placement-editor=ready]'),'gesture engine');baseline=(await state()).source;revision=(await state()).revision;
  checks.push('task-owned actual arm64 WKWebView with null audio/input off; all interaction input explicitly synthetic');
  change('[name=placementZoom]','96');
  for(const [step,expected] of [['3840',3840],['960',960],['480',480],['320',960],['240',240],['160',480],['120',240],['1',240]]){change('[name=placementSnap]',step);const count=posts.length;key('ArrowRight','ArrowRight',block().querySelector('.placement-select'));await value('at',3840+expected);if(posts.length!==count+1)throw Error('Nudge write count '+step);await undo();if(node('[name=placementSnap]').value!==step)throw Error('Snap reset')}
  checks.push('all snap settings move patterns on compatible sixteenth boundaries; each submits once and exact Undo restores source/revision');
  change('[name=placementSnap]','320');let count=posts.length;key('ArrowRight','ArrowRight',block().querySelector('[data-placement-resize]'));await value('length',4160);if(posts.length!==count+1)throw Error('Trim write count');await undo();checks.push('triplet trim adds exactly320 ticks with one real save and exact Undo');
  change('[name=placementSnap]','240');count=posts.length;let release=held({alt:true});if(!document.querySelector('.placement-copy-origin')||!document.querySelector('[data-placement-copy-preview]'))throw Error('Copy preview absent '+JSON.stringify({status:document.querySelector('[data-placement-edit-status]')?.textContent,engine:document.querySelector('#studio-shell')?.dataset.shellPlacementEditor,drag:document.querySelector('[data-placement-dragging]')?.outerHTML,rect:block().getBoundingClientRect().toJSON()}));if((await state()).source!==baseline||posts.length!==count)throw Error('Held preview saved');key('ArrowRight','ArrowRight',block().querySelector('.placement-select'));key('d','KeyD',block().querySelector('.placement-select'),{metaKey:true});if(posts.length!==count)throw Error('Held keyboard race');release();await value('at',7680,'verse-copy');if(posts.length!==count+1||block().getAttribute('data-placement-at')!=='3840'||block('verse-copy').getAttribute('data-placement-selected')!=='true')throw Error('Copy save selection/count');await wait(()=>document.activeElement===block('verse-copy').querySelector('.placement-select'),'copy focus');await undo();checks.push('synthetic Option-drag keeps original visible, blocks held keyboard races and saves one selected/focused copy with exact Undo');
  count=posts.length;release=held({alt:true});key('Escape','Escape',block().querySelector('.placement-select'));release();await pause(200);if(posts.length!==count||(await state()).source!==baseline||document.querySelector('.placement-copy-origin,[data-placement-dragging],[data-placement-copy-preview]'))throw Error('Cancelled copy leaked');checks.push('Escape cancels copy preview without writes or leftover gesture state');
  change('[name=placementCopy]',true);count=posts.length;release=held();release();await value('at',7680,'verse-copy');if(posts.length!==count+1)throw Error('Copy mode write count');await undo();if(!node('[name=placementCopy]').checked)throw Error('Copy mode reset');checks.push('modifier-free Copy drag saves one copy and persists through exact Undo');
  count=posts.length;release=held({resize:true,dx:-24});release();await value('length',2880);if(posts.length!==count+1||document.querySelector('[data-placement-block=verse-copy]'))throw Error('Edge copied instead of trimmed');await undo();checks.push('Copy drag keeps edge gestures as trims');
  node('#view-tab-session').click();node('#view-tab-arrange').click();if(!node('[name=placementCopy]').checked||node('[name=placementSnap]').value!=='240')throw Error('Preferences lost on tab change');change('[name=placementCopy]',false);if((await state()).source!==baseline||(await state()).revision!==revision||errors.length)throw Error('Final source/revision or runtime errors: '+errors.join('; '));checks.push('Arrange/Session preserves preferences; final source/revision exact, no runtime errors');
  return {type:'cicada-native-smoke',ok:true,checks,arrangePOSTs:posts.length,pointerInput:'synthetic DOM PointerEvent; no capture shim',keyboardInput:'synthetic DOM KeyboardEvent; real action requests',physicalAudioVerified:false};
 })().catch(e=>({type:'cicada-native-smoke',ok:false,checks,error:String(e)+'\n'+e.stack,errors})).then(result=>window.chrome.webview.postMessage(JSON.stringify(result)));
})();
