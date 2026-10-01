const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const test = require('node:test');
const html = fs.readFileSync(__dirname + '/view.html', 'utf8');
const refreshSource = html.match(/  async function refreshProjection\(hint = \{\}\) \{[\s\S]*?\n  \}/)[0];
const sendSource = html.match(/  async function send\(path, payload\) \{[\s\S]*?\n  \}/)[0];

test('Studio navigation hides section links without hiding the actionable error chip', () => {
  assert.match(html, /body\[data-studio\] \.nav a:not\(\.bar-error\)\s*\{\s*display:none;\s*\}/);
  assert.doesNotMatch(html, /body\[data-studio\] \.nav a\s*\{\s*display:none;/);
  assert.match(html, /a\.bar-error\[hidden\]\s*\{\s*display:none;\s*\}/);
});

test('toolbar error chip appears on error and clears on success', () => {
  const update = html.match(/  function updateBar\(\) \{[\s\S]*?\n  \}/)[0];
  const state = {
    status: {dataset: {state: 'error'}, textContent: 'chord already has 4 pitches; remove a pitch before adding another'},
    statusKind: '', dirty: () => false, barSave: {dataset: {}}, barError: {hidden: true},
  };
  vm.runInNewContext(update, state);
  state.updateBar();
  assert.equal(state.barError.hidden, false);
  assert.match(state.barError.textContent, /remove a pitch/);
  assert.equal(state.barError.title, state.status.textContent);
  state.status.dataset.state = 'success';
  state.status.textContent = 'Saved';
  state.updateBar();
  assert.equal(state.barError.hidden, true);
  assert.equal(state.barError.textContent, '');
  assert.equal(state.barError.title, '');
});

function cell(dataset) {
  return {dataset, classList: {contains: name => name === 'step-edit'}, focus() { this.focused = true; }};
}

for (const target of [{pitch:'65'}, {modifier:'accent'}, {}]) {
  test(`projection refresh keeps exact grid identity ${JSON.stringify(target)}`, async () => {
    const identity = {pattern:'harmony', step:'0'};
    const old = cell({...identity, ...target});
    const cells = [cell({...identity,pitch:'74'}), cell({...identity,pitch:'65'}), cell({...identity,modifier:'accent'}), cell(identity)];
    const state = {
      document: {activeElement:old, body:{dataset:{}}, querySelectorAll: selector => selector === '.step-edit' ? cells : []},
      fetch: async () => ({ok:true, text:async () => '', json:async () => ({source:'score'})}),
      DOMParser: class { parseFromString() { return {body:{dataset:{revision:'next'}}}; } },
      replaceProjection() {}, morphSelectors:[], bindSong() {}, initGrids() {}, dirty: () => false,
      original:'score', editor:{value:'score'}, setRovingTarget: cell => state.roving = cell,
      updateBar() {}, window:{dispatchEvent() {}}, CustomEvent: class {},
    };
    vm.runInNewContext(refreshSource,state);
    await state.refreshProjection();
    const wanted = cells.find(c => c.dataset.pitch === target.pitch && c.dataset.modifier === target.modifier);
    assert.equal(state.roving,wanted);
    assert.equal(wanted.focused,true);
    assert.equal(cells.filter(c => c.focused).length,1);
  });
}

test('when a removed outer pitch row disappears, focus falls back to the same step Note cell', async () => {
  const note = cell({pattern:'harmony',step:'0'});
  const otherPitch = cell({pattern:'harmony',step:'0',pitch:'62'});
  const cells = [otherPitch,cell({pattern:'other',step:'0'}),note];
  const state = {
    document:{activeElement:cell({pattern:'harmony',step:'0',pitch:'84'}),body:{dataset:{}},querySelectorAll:selector => selector === '.step-edit' ? cells : []},
    fetch:async () => ({ok:true,text:async () => '',json:async () => ({source:'score'})}),
    DOMParser:class {parseFromString(){return {body:{dataset:{revision:'next'}}};}},
    replaceProjection(){},morphSelectors:[],bindSong(){},initGrids(){},dirty:()=>false,
    original:'score',editor:{value:'score'},setRovingTarget:cell => state.roving=cell,
    updateBar(){},window:{dispatchEvent(){}},CustomEvent:class {},
  };
  vm.runInNewContext(refreshSource,state);
  await state.refreshProjection();
  assert.equal(state.roving,note);
  assert.equal(note.focused,true);
  assert.equal(otherPitch.focused,undefined);
});

test('fifth chord pitch rejection reaches the visible error status and retains source revision', async () => {
  const error = 'chord already has 4 pitches; remove a pitch before adding another';
  const state = {
    original:'score',busy:false,editor:{value:'score'},save:{disabled:false},
    document:{body:{dataset:{revision:'original'}}},revision:()=> 'original',
    fetch:async () => ({ok:false,json:async () => ({error})}),
    setStatus:(message,kind)=>state.status={message,kind},refreshProjection:()=>{throw Error('must not refresh');},
  };
  vm.runInNewContext(sendSource,state);
  assert.equal(await state.send('/api/toggle',{pattern:'harmony',step:0,pitch:74}),false);
  assert.deepEqual(state.status,{message:error,kind:'error'});
  assert.equal(state.document.body.dataset.revision,'original');
  assert.equal(state.editor.value,'score');
  assert.equal(state.save.disabled,false);
  assert.equal(state.busy,false);
});
