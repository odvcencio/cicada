export async function createGui(plugin) {
  const root = document.createElement('section');
  const shadow = root.attachShadow({ mode: 'open' });
  shadow.innerHTML = `<style>
    :host{display:block;width:100%;max-width:440px;color:#eee8d9;font:15px system-ui,sans-serif}
    *{box-sizing:border-box}section{background:#182525;border:1px solid #405454;border-radius:14px;padding:24px}
    .brand{color:#9bd6bc;font-size:11px;font-weight:700;letter-spacing:.16em;text-transform:uppercase}
    h2{font-size:25px;line-height:1.2;margin:9px 0 8px;overflow-wrap:anywhere}p{color:#c3ccc5;line-height:1.5;margin:0 0 22px}
    button{border:1px solid #8cb7a4;background:#9bd6bc;color:#152422;border-radius:6px;padding:10px 16px;font:inherit;font-weight:650;cursor:pointer}
    button:disabled{opacity:.6;cursor:wait}button:focus-visible,input:focus-visible{outline:3px solid #edcb86;outline-offset:4px}
    .status{margin-left:12px;font-size:13px;color:#c3ccc5}label{display:block;margin-top:24px;font-weight:600;overflow-wrap:anywhere}
    .value{float:right;color:#9bd6bc;font-variant-numeric:tabular-nums}input{display:block;width:100%;margin:14px 0 0;accent-color:#9bd6bc;cursor:pointer}
    .error{color:#ffbaa7;margin:16px 0 0;font-size:13px}.empty{margin:24px 0 0;font-size:13px}
    @media(max-width:360px){section{padding:18px}h2{font-size:22px}}
  </style><section><div class="brand">Cicada · Instrument</div><h2></h2><p>Play the score or send MIDI from your host.</p><button type="button">Play score</button><span class="status" aria-live="polite">Stopped</span><div class="macros"></div><p class="error" role="alert" hidden></p></section>`;
  shadow.querySelector('h2').textContent = plugin.name;
  const node = plugin.audioNode;
  const button = shadow.querySelector('button');
  const status = shadow.querySelector('.status');
  const error = shadow.querySelector('.error');
  const controls = new Map();
  let disposed = false, updating = false, playing = false;
  const fail = reason => { error.hidden = false; error.textContent = reason.message || String(reason); };
  const onFault = event => fail(event.detail);
  node.addEventListener('cicada-error', onFault);
  const info = await node.getParameterInfo();
  for (const [id, parameter] of Object.entries(info)) {
    const label = document.createElement('label');
    label.append(document.createTextNode(parameter.label));
    const value = document.createElement('span');
    value.className = 'value';
    const slider = document.createElement('input');
    slider.type = 'range'; slider.min = parameter.minValue; slider.max = parameter.maxValue; slider.step = '0.001';
    slider.setAttribute('aria-label', parameter.label);
    slider.oninput = () => {
      value.textContent = Number(slider.value).toFixed(3);
      node.setParameterValues({ [id]: { id, value: Number(slider.value), normalized: false } }).catch(fail);
    };
    label.append(value, slider);
    shadow.querySelector('.macros').append(label);
    controls.set(id, { slider, value });
  }
  if (!controls.size) {
    const empty = document.createElement('p'); empty.className = 'empty';
    empty.textContent = 'This score has no live macros.';
    shadow.querySelector('.macros').append(empty);
  }
  const refresh = async () => {
    if (disposed || updating) return;
    updating = true;
    try {
      const state = await node.getState();
      if (disposed) return;
      playing = state.playing;
      button.textContent = playing ? 'Stop score' : 'Play score';
      status.textContent = playing ? 'Playing' : 'Stopped';
      for (const [id, control] of controls) {
        control.slider.value = state.parameterValues[id].value;
        control.value.textContent = Number(control.slider.value).toFixed(3);
      }
    } catch (reason) { if (!disposed) fail(reason); }
    finally { updating = false; }
  };
  button.onclick = async () => {
    button.disabled = true;
    try {
      await plugin.audioContext.resume();
      const state = await node.getState();
      await node.setState({ ...state, playing: !state.playing });
      await refresh();
    } catch (reason) { fail(reason); }
    finally { button.disabled = false; }
  };
  await refresh();
  const timer = setInterval(refresh, 200);
  root.dispose = () => { disposed = true; clearInterval(timer); node.removeEventListener('cicada-error', onFault); };
  return root;
}
