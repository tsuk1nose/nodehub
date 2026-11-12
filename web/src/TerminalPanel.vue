<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { t } from './i18n'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
const props=defineProps<{serial:string}>()
const emit=defineEmits<{close:[]}>()
const host=ref<HTMLDivElement|null>(null), status=ref('Establishing session…')
const statusText=()=>status.value.split(' · ').map(part=>t(part)).join(' · ')
let term:Terminal|undefined, fit:FitAddon|undefined, socket:WebSocket|undefined, observer:ResizeObserver|undefined
let disposed=false
function send(v:unknown){if(socket?.readyState===WebSocket.OPEN)socket.send(JSON.stringify(v))}
function resize(){if(!term||!fit||disposed)return;fit.fit();send({type:'resize',cols:Math.min(500,Math.max(2,term.cols)),rows:Math.min(300,Math.max(1,term.rows))})}
onMounted(async()=>{
 term=new Terminal({cursorBlink:true,fontSize:14,scrollback:2000,theme:{background:'#10231e',foreground:'#e4eee3'},allowProposedApi:false})
 fit=new FitAddon();term.loadAddon(fit);term.open(host.value!);fit.fit()
 term.onData(data=>{ // Bound each message by UTF-8 bytes, not JavaScript UTF-16 length.
  let chunk='',bytes=0;for(const character of data){const size=new TextEncoder().encode(character).length;if(bytes+size>4096){send({type:'input',data:chunk});chunk='';bytes=0};chunk+=character;bytes+=size};if(chunk)send({type:'input',data:chunk})
 })
 observer=new ResizeObserver(resize);observer.observe(host.value!)
 try{
  const response=await fetch(`/api/nodes/${encodeURIComponent(props.serial)}/terminal`,{method:'POST',headers:{'Content-Type':'application/json','X-NodeHub-Request':'1'},body:'{}'})
  const body=await response.json();if(!response.ok)throw Error(body.error||'Cannot create terminal');if(disposed)return
  const url=new URL(`/api/terminal/${body.session_id}/ws`,location.href);url.protocol=location.protocol==='https:'?'wss:':'ws:'
  socket=new WebSocket(url);socket.binaryType='arraybuffer';socket.onopen=()=>{resize();term?.focus()}
  socket.onmessage=event=>{if(disposed)return;if(typeof event.data==='string'){try{const m=JSON.parse(event.data);if(m.type==='status')status.value=m.message}catch{status.value='Invalid message received';socket?.close()}}else{const bytes=new Uint8Array(event.data);term?.write(bytes,()=>send({type:'ack',bytes:bytes.byteLength}))}}
  socket.onerror=()=>{status.value='Connection failed; check the Agent, SSH configuration, and allowed origins'}
  socket.onclose=()=>{if(!disposed)status.value=status.value==='Connected'||status.value==='Establishing session…'?'Session ended':status.value+' · Session ended'}
 }catch(e){status.value=(e as Error).message}
})
onUnmounted(()=>{disposed=true;observer?.disconnect();socket?.close();term?.dispose()})
</script>
<template><div class="terminal-backdrop"><section class="terminal-panel" role="dialog" aria-modal="true" :aria-label="serial+' '+t('native terminal')"><header><strong class="mono">{{serial}} · SSH</strong><span>{{statusText()}}</span><button class="plain" @click="emit('close')">{{ t('Close terminal ×') }}</button></header><div class="terminal-host" ref="host"></div><p>{{ t('SSH relayed through the node Agent · Closing this window ends the session') }}</p></section></div></template>
<style scoped>
.terminal-backdrop{position:fixed;inset:0;background:#10231ed9;z-index:30;display:grid;place-items:center;padding:24px}.terminal-panel{display:flex;flex-direction:column;width:min(1200px,100%);height:min(820px,92vh);background:#10231e;color:#e4eee3;border:1px solid #426151;border-radius:12px;overflow:hidden}.terminal-panel header{height:58px;flex-shrink:0;display:flex;align-items:center;gap:20px;padding:0 20px;border-bottom:1px solid #345043}.terminal-panel header span{font-size:12px;color:#a7b9a9;flex:1}.terminal-panel p{font-size:11px;color:#8fa897;padding:12px 20px;margin:0}.terminal-host{flex:1;min-height:0;overflow:hidden;padding:14px}.terminal-host :deep(.xterm){height:100%}@media(max-width:650px){.terminal-backdrop{padding:4px}.terminal-panel header{gap:8px;padding:0 10px}.terminal-panel header span{display:none}.terminal-panel header strong{flex:1;font-size:12px}}
</style>
