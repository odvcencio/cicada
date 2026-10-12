/* Use a fresh, private edition-2 eight-bar score with no audio track, served
 * through --audio null. This test never enables an actual microphone. */
'use strict';
const test=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path');
test('Adding a vocal track enables recording controls; Audio retains the Session route',{timeout:60000},async t=>{
 const base=process.env.CICADA_SETUP_URL;assert.ok(base);assert.equal(new URL(base).hostname,'127.0.0.1');
 const {chromium}=require(process.env.CICADA_PLAYWRIGHT_MODULE||'playwright');
 const browser=await chromium.launch({headless:true,executablePath:process.env.CICADA_CHROME_PATH,args:['--no-sandbox']});t.after(()=>browser.close());
 const context=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,reducedMotion:'reduce'}),page=await context.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
 const ready=async()=>{await page.locator('#cicada-workspace[data-workspace-reactive=ready]').waitFor();await page.locator('#cicada-workspace[aria-busy=true]').waitFor({state:'hidden'})};
 await page.goto(base+'/?panel=session',{waitUntil:'domcontentloaded'});await ready();const initial=await(await page.request.get(base+'/api/state')).json();assert.ok(initial.source.includes('cicada-experience-test: disposable fixture'));
 await page.waitForTimeout(600);assert.equal(await page.getByRole('button',{name:'Record take',exact:true}).isDisabled(),true);assert.ok((await page.locator('[data-capture-status]').innerText()).includes('Add a vocal track'));
 await page.getByRole('button',{name:'Add vocal track',exact:true}).click();await ready();await page.waitForTimeout(900);assert.equal(await page.getByRole('button',{name:'Record take',exact:true}).isDisabled(),false,'capture polling reapplied the old empty-track state');assert.equal(await page.getByLabel('Vocal track',{exact:true}).inputValue(),'danilo');
 await page.getByRole('link',{name:'Choose microphone and output',exact:true}).click();await page.locator('#audio').waitFor();assert.equal(new URL(page.url()).searchParams.get('vocalTrack'),'danilo');assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true);
 const proof=process.env.CICADA_VOCAL_PROOF_DIR;if(proof){fs.mkdirSync(proof,{recursive:true});await page.locator('#audio').screenshot({path:path.join(proof,'audio-setup-390.png')})}
 await page.getByRole('button',{name:'Apply and return to Session',exact:true}).click();await page.locator('#vocals').waitFor();await ready();assert.equal(new URL(page.url()).searchParams.get('panel'),'session');assert.equal(new URL(page.url()).searchParams.get('vocalTrack'),'danilo');assert.equal(await page.getByRole('button',{name:'Record take',exact:true}).isDisabled(),false);assert.deepEqual(errors,[]);
 const final=await(await page.request.get(base+'/api/state')).json();assert.ok(final.source.includes('track danilo audio'));assert.ok(final.source.includes('track bass acid'));const audio=await(await page.request.get(base+'/api/audio/config')).json();assert.equal(audio.options.inputEnabled,false);
 console.log('Phone setup, post-add recording controls, named audio setup, Session return, and disabled microphone verified.');
});
