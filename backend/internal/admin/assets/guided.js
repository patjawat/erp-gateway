"use strict";
(() => {
  const ui = {authenticated:false,screen:"overview",epoch:0,dashboard:null,selected:new Set(),updateConfigured:false,setupCSRF:"",setupStep:0,setupValid:false,setupEnabled:true,setupLANHTTPEnabled:false,setupLocalHTTPEnabled:false,setupURLTouched:false,settingsLANHTTPEnabled:false,settingsLocalHTTPEnabled:false,settingsBase:null,settingsEditable:false,settingsDirty:false,settingsDraft:null,settingsSaving:false,recoveryBusy:false,recoveryController:null};
  const pages = new Map();
  const number = value => value === null || value === undefined ? "ยังไม่มีข้อมูล" : new Intl.NumberFormat("th-TH").format(value);
  const paths = {home:"M3 10 12 3l9 7v11h-6v-7H9v7H3Z",building:"M5 21V3h14v18M9 7h1m4 0h1M9 11h1m4 0h1M9 15h1m4 0h1M10 21v-3h4v3",download:"M12 3v12m-5-5 5 5 5-5M4 15v6h16v-6",shield:"M12 3 3 6v6c0 5 9 9 9 9s9-4 9-9V6Zm-4 9 3 3 5-6",settings:"M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1Z",arrow:"M4 12h15m-6-6 6 6-6 6",clock:"M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18m0 4v5l3 2",sun:"M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5",check:"m5 12 4 4L19 6",users:"M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2m20 0v-2a4 4 0 0 0-3-3.9M9 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8m7 .1a4 4 0 0 1 0 7.8",activity:"M2 12h4l3-8 6 16 3-8h4"};
  function icon(name) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg","svg");
    svg.setAttribute("viewBox","0 0 24 24"); svg.setAttribute("class","icon"); svg.setAttribute("aria-hidden","true");
    const path = document.createElementNS("http://www.w3.org/2000/svg","path");
    path.setAttribute("d",paths[name] || paths.shield); svg.append(path); return svg;
  }
  document.querySelectorAll("[data-icon]").forEach(el => el.replaceChildren(icon(el.dataset.icon)));
  function setTheme(mode) {
    document.documentElement.dataset.theme = mode;
    byId("theme-label").textContent = mode === "dark" ? "โหมดสว่าง" : "โหมดมืด";
    byId("theme-toggle").setAttribute("aria-label","เปลี่ยนเป็น"+(mode === "dark" ? "โหมดสว่าง" : "โหมดมืด"));
    try {localStorage.setItem("erp.gateway.theme",mode);} catch (_) {}
  }
  let savedTheme = "light"; try {savedTheme = localStorage.getItem("erp.gateway.theme") === "dark" ? "dark" : "light";} catch (_) {}
  setTheme(savedTheme);
  function toggleTheme(){setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark");}
  byId("theme-toggle").addEventListener("click",toggleTheme);
  byId("settings-theme").addEventListener("click",toggleTheme);
  function setError(id,error) {const el=byId(id);el.textContent=error?.message || String(error || "");el.hidden=!error;}
  function merge(items,next) {const map=new Map(items.map(item=>[String(item.id ?? item.hospital_id),item]));for(const item of next) map.set(String(item.id ?? item.hospital_id),item);return [...map.values()];}
  function registerPage(key,config) {
    const page={...config,key,items:[],cursor:null,total:0,loaded:false,loading:false,error:false,generation:0,controller:null};
    pages.set(key,page);
    byId(config.button)?.addEventListener("click",()=>loadPage(key,!page.loaded));
    return page;
  }
  function pageButton(page) {
    const button=byId(page.button); if(!button)return;
    button.hidden=!page.cursor && !page.error && !page.loading;
    button.disabled=page.loading;
    button.textContent=page.loading?"กำลังโหลด…":page.error?"ลองโหลดอีกครั้ง":page.label;
  }
  async function loadPage(key,reset=false) {
    const page=pages.get(key);
    if(!ui.authenticated || ui.screen!==page.screen || (page.loading && !reset) || (!reset && page.loaded && !page.cursor))return;
    if(reset){page.controller?.abort();page.generation++;page.loading=false;page.cursor=null;page.loaded=false;page.error=false;}
    const generation=page.generation,epoch=ui.epoch;
    page.loading=true;page.error=false;page.controller=new AbortController();pageButton(page);
    const params=new URLSearchParams({limit:String(page.limit || 25),...page.params?.()});
    if(page.cursor && !reset)params.set("cursor",page.cursor);
    try {
      const result=await api(page.path+"?"+params,{signal:page.controller.signal});
      if(generation!==page.generation || epoch!==ui.epoch)return;
      page.items=merge(reset?[]:page.items,result[page.collection] || []);
      page.cursor=result.next_cursor || null;page.total=result.total ?? page.items.length;page.loaded=true;
      page.render(page.items,result);
    } catch(error) {
      if(generation!==page.generation || epoch!==ui.epoch)return;
      page.error=true;notice(error.message,"error");
      if(!page.items.length)page.render([],null);
    } finally {
      if(generation===page.generation && epoch===ui.epoch){page.loading=false;pageButton(page);}
    }
  }
  function invalidate(key) {const p=pages.get(key);if(p){p.loaded=false;p.generation++;p.controller?.abort();p.loading=false;}}
  function onVisibleMore(entries) {
    for(const entry of entries){if(!entry.isIntersecting)continue;const key=entry.target.dataset.pageKey;const p=pages.get(key);if(p && p.screen===ui.screen && p.loaded && p.cursor && !p.loading && !p.error)loadPage(key);}
  }
  const observer="IntersectionObserver" in window ? new IntersectionObserver(onVisibleMore,{rootMargin:"160px"}) : null;
  registerPage("hospitals",{screen:"hospitals",path:"hospitals",collection:"hospitals",button:"hospitals-more",label:"โหลดหน่วยงานเพิ่ม",params:()=>({q:byId("search").value.trim(),status:byId("filter").value}),render:(items,result)=>{state.hospitals=items;state.hospitalTotal=result?.total ?? 0;renderHospitals();}});
  registerPage("audit",{screen:"overview",path:"audit",collection:"events",limit:10,button:"audit-more",label:"โหลดกิจกรรมเพิ่ม",render:items=>renderAudit(items)});
  registerPage("releases",{screen:"updates",path:"updates/releases",collection:"releases",button:"releases-more",label:"โหลดรุ่นเพิ่ม",render:renderReleases});
  registerPage("jobs",{screen:"updates",path:"updates/jobs",collection:"jobs",button:"jobs-more",label:"โหลดงานเพิ่ม",render:renderJobs});
  registerPage("agents",{screen:"updates",path:"updates/agents",collection:"agents",button:"agents-more",label:"โหลดเครื่องเพิ่ม",render:renderAgents});
  registerPage("updateHospitals",{screen:"updates",path:"hospitals",collection:"hospitals",button:"update-hospitals-more",label:"โหลดหน่วยงานเพิ่ม",params:()=>({q:byId("update-search").value.trim()}),render:renderUpdateHospitals});
  for(const key of ["hospitals","audit","jobs","agents"]){const el=byId(key+"-sentinel");if(el){el.dataset.pageKey=key;observer?.observe(el);}}
  let searchTimer,updateSearchTimer;
  function hospitalsChanged(immediate=false){clearTimeout(searchTimer);searchTimer=setTimeout(()=>loadPage("hospitals",true),immediate?0:300);}
  async function showScreen(screen,{refresh=false}={}) {
    if(!["overview","hospitals","updates","certificates","settings"].includes(screen))screen="overview";
    if(!ui.authenticated)return;
    const previous=ui.screen;ui.screen=screen;if(previous!==screen)notice("");
    if(previous==="settings"&&screen!=="settings"){ui.recoveryController?.abort();byId("recovery-export-form").reset();}
    document.querySelectorAll("[data-view]").forEach(el=>el.hidden=el.dataset.view!==screen);
    document.querySelectorAll(".rail-button").forEach(el=>{el.classList.toggle("active",el.dataset.nav===screen);el.setAttribute("aria-current",el.dataset.nav===screen?"page":"false");});
    document.title=({"overview":"ภาพรวม","hospitals":"จัดการหน่วยงาน","updates":"อัปเดต API","certificates":"ใบรับรอง","settings":"ตั้งค่า"}[screen])+" · ERP Mobile Gateway";
    if(screen!=="hospitals"){closeAttachments(false);setAddOpen(false);}
    const tasks=[];
    if(screen==="overview"){tasks.push(loadDashboard(),loadOverviewHospitals());if(refresh || !pages.get("audit").loaded)tasks.push(loadPage("audit",true));}
    if(screen==="hospitals" && (refresh || !pages.get("hospitals").loaded))tasks.push(loadPage("hospitals",true));
    if(screen==="updates"){tasks.push(loadUpdateStatus());for(const key of ["releases","jobs","agents","updateHospitals"]){if(key==="updateHospitals" && byId("update-targets").hidden)continue;if(refresh || !pages.get(key).loaded)tasks.push(loadPage(key,true));}}
    if(screen==="certificates")tasks.push(loadCertificates());
    if(screen==="settings")tasks.push(loadSettings());
    await Promise.allSettled(tasks);
  }
  document.addEventListener("click",event=>{const target=event.target.closest("[data-nav]");if(target){event.preventDefault();showScreen(target.dataset.nav);}});
  window.gatewayUI={
    authenticated(value){
      ui.authenticated=value;ui.epoch++;
      byId("main-rail").hidden=!value;
      document.querySelector(".app-shell").classList.toggle("is-authenticated",value);
      if(!value){
        for(const p of pages.values()){p.controller?.abort();p.generation++;p.loading=false;p.loaded=false;p.items=[];p.cursor=null;}
        ui.selected.clear();ui.dashboard=null;byId("updater-token-value").value="";
        ui.settingsBase=null;ui.settingsDraft=null;ui.settingsDirty=false;ui.settingsEditable=false;ui.settingsSaving=false;ui.recoveryController?.abort();ui.recoveryBusy=false;byId("settings-form").reset();byId("settings-review").hidden=true;byId("recovery-export-form").reset();for(const id of ["settings-public-url","settings-proxies","settings-upstreams","settings-private-cidrs","settings-reset","settings-review-submit","settings-refresh","recovery-export-submit"])byId(id).disabled=false;byId("settings-apply").disabled=true;
        if(byId("token-dialog").open)byId("token-dialog").close();
      } else {ui.screen="overview";document.querySelectorAll("[data-view]").forEach(el=>el.hidden=el.dataset.view!=="overview");}
    },
    refresh:()=>showScreen(ui.screen,{refresh:true}),hospitalsChanged,invalidate
  };
  function metric(label,value,unit,detail,ic) {
    const el=node("div","metric-item"),title=node("div","metric-label");title.append(icon(ic),document.createTextNode(label));
    const val=node("div","metric-value",value === null || value === undefined ? "—" : number(value));
    if(unit && value!==null && value!==undefined)val.append(node("small","",unit));
    el.append(title,val,node("div","metric-detail",detail));return el;
  }
  let dashboardRequest=0,overviewRequest=0,certificateRequest=0,settingsRequest=0;
  async function loadDashboard() {
    const request=++dashboardRequest,epoch=ui.epoch;
    try {
      const data=await api("dashboard");if(request!==dashboardRequest || epoch!==ui.epoch)return;
      ui.dashboard=data;const h=data.hospitals || {},u=data.usage || {};
      byId("dashboard-metrics").replaceChildren(
        metric("หน่วยงานที่ดูแล",h.total,"แห่ง","เชื่อมต่อ "+number(h.online)+" แห่ง","building"),
        metric("บุคลากรปัจจุบัน",u.personnel,"คน",u.personnel==null?"รอข้อมูลสรุปจากหน่วยงาน":"ข้อมูลจากหน่วยงานที่รายงาน","users"),
        metric("ใช้แอปใน 30 วัน",u.active_30d,"คน",u.active_30d==null?"ยังไม่มีข้อมูลผู้ใช้งาน":"ผู้ใช้งานไม่ซ้ำในช่วงเวลา","activity"),
        metric("สัดส่วนการใช้งาน",u.adoption_30d,"%",u.adoption_30d==null?"ยังคำนวณสัดส่วนไม่ได้":number(u.active_30d)+" / "+number(u.personnel)+" คน","users"));
      byId("usage-coverage").textContent="ข้อมูลบุคลากรและการใช้แอปจาก "+number(u.coverage?.reporting ?? 0)+" / "+number(u.coverage?.total ?? h.total ?? 0)+" หน่วยงาน";
      byId("usage-note").textContent=u.status==="not_configured" ? "ยังไม่มีข้อมูลการใช้แอปจากหน่วยงาน จึงยังไม่แสดงจำนวนคนหรือสัดส่วนการใช้งาน จำนวนคน การติดตั้ง และคำขอ API เป็นคนละตัวชี้วัด" : (u.status==="stale"?"ข้อมูลบางส่วนล่าช้า · ":"")+"การติดตั้ง Android "+number(u.android_30d)+" เครื่อง · iOS "+number(u.ios_30d)+" เครื่อง ใน 30 วัน คนเดียวมีหลายเครื่องได้";
      byId("last-updated").textContent="ข้อมูลสรุปล่าสุด "+when(data.as_of);
      const tasks=[];
      if(h.pending)tasks.push({title:number(h.pending)+" หน่วยงานรออนุมัติ",desc:"ทดสอบการเชื่อมต่อก่อนเปิดใช้งาน",screen:"hospitals",ic:"building"});
      if(h.offline)tasks.push({title:number(h.offline)+" หน่วยงานติดต่อไม่ได้",desc:"ตรวจการเชื่อมต่อล่าสุดก่อนส่งงาน",screen:"hospitals",ic:"activity"});
      const cert=data.certificates?.gateway;
      if(!cert)tasks.push({title:"ตรวจใบรับรอง Gateway",desc:"ยังไม่มีข้อมูลใบรับรองพร้อมใช้งาน",screen:"certificates",ic:"shield"});
      else if(cert.days_remaining<=30)tasks.push({title:"ใบรับรองเหลือ "+number(cert.days_remaining)+" วัน",desc:"เตรียมคำขอต่ออายุและนำเข้าใบใหม่",screen:"certificates",ic:"shield"});
      if(data.certificates?.pending_requests)tasks.push({title:"มีคำขอใบรับรองรอลงนาม",desc:"นำเข้าใบที่ลงนามแล้วเมื่อพร้อม",screen:"certificates",ic:"shield"});
      byId("attention-count").textContent=number(tasks.length);
      byId("attention-tasks").replaceChildren(...tasks.map(task=>{const b=node("button","task");b.type="button";b.dataset.nav=task.screen;const symbol=node("span","task-symbol");symbol.append(icon(task.ic));const copy=node("span");copy.append(node("strong","",task.title),node("small","",task.desc));b.append(symbol,copy,icon("arrow"));return b;}));
      if(!tasks.length)byId("attention-tasks").append(node("p","task-empty","ไม่มีงานที่ต้องติดตามจากข้อมูลล่าสุด"));
    }catch(error){if(request===dashboardRequest && epoch===ui.epoch)notice(error.message,"error");}
  }
  async function loadOverviewHospitals(){
    const request=++overviewRequest,epoch=ui.epoch;
    try{const data=await api("hospitals?limit=5");if(request!==overviewRequest||epoch!==ui.epoch)return;const rows=(data.hospitals||[]).map(h=>{const row=node("div","overview-hospital"),symbol=node("span","task-symbol");symbol.append(icon("building"));const copy=node("div");copy.append(node("div","hospital-name",h.name),node("small","",h.code+" · "+(h.province||"ไม่ระบุจังหวัด")));const status=h.connection_status==="online"?"เชื่อมต่อได้":h.connection_status==="offline"?"ติดต่อไม่ได้":"ยังไม่ได้ทดสอบ";row.append(symbol,copy,badge(status,h.connection_status==="online"?"success":"neutral"));return row;});byId("overview-hospitals").replaceChildren(...rows);if(!rows.length)byId("overview-hospitals").append(node("p","muted","ยังไม่มีหน่วยงาน เพิ่มหน่วยงานเพื่อเริ่มทดสอบและอนุมัติ"));}catch(error){if(epoch===ui.epoch)byId("overview-hospitals").replaceChildren(node("p","muted",error.message));}
  }
  byId("dashboard-refresh").addEventListener("click",()=>showScreen("overview",{refresh:true}));
  function summary(rows){const dl=document.createDocumentFragment();for(const [label,value] of rows){const row=node("div");row.append(node("dt","",label),node("dd","",String(value??"ยังไม่มีข้อมูล")));dl.append(row);}return dl;}
  const settingsFields={public_url:"settings-public-url",trusted_proxy_cidrs:"settings-proxies",approved_upstreams:"settings-upstreams",private_upstream_cidrs:"settings-private-cidrs"};
  const settingsLabels={public_url:"ที่อยู่ Gateway",trusted_proxy_cidrs:"เครือข่าย Reverse proxy ที่เชื่อถือ",approved_upstreams:"ปลายทาง API ที่อนุญาต",private_upstream_cidrs:"เครือข่ายภายในของปลายทาง"};
  function privateIPLiteral(host) {
    if(host.startsWith("["))return /^\[f[cd][0-9a-f]{2}:/i.test(host);
    const parts=host.split(".");if(parts.length!==4||parts.some(part=>!/^(0|[1-9]\d{0,2})$/.test(part)||Number(part)>255))return false;
    const [first,second]=parts.map(Number);return first===10||(first===172&&second>=16&&second<=31)||(first===192&&second===168);
  }
  function localHTTPHost(host,normalizedHost) {
    if(host.toLowerCase()==="localhost")return true;
    if(host.startsWith("["))return normalizedHost==="[::1]";
    const parts=host.split(".");return parts.length===4&&parts[0]==="127"&&parts.every(part=>/^(0|[1-9]\d{0,2})$/.test(part)&&Number(part)<=255);
  }
  function gatewayURLValid(value,allowLANHTTP,allowLocalHTTP=false) {
    try {
      const url=new URL(value),host=value.match(/^https?:\/\/(\[[^\]]+\]|[^:/?#]+)(?::\d+)?\/?$/i)?.[1];
      return Boolean(host)&&(url.protocol==="https:"||(url.protocol==="http:"&&((allowLANHTTP&&privateIPLiteral(host))||(allowLocalHTTP&&localHTTPHost(host,url.hostname)))))&&Boolean(url.hostname)&&!url.username&&!url.password&&url.pathname==="/"&&!url.search&&!url.hash&&url.port!=="0"&&!/[\\\r\n]/.test(value);
    }catch(_){return false;}
  }
  function gatewayURLMessage(allowLANHTTP,allowLocalHTTP=false) {
    const scope=allowLANHTTP?(allowLocalHTTP?"IP ภายในหรือ localhost":"IP ภายใน"):"localhost หรือ loopback";
    return allowLANHTTP||allowLocalHTTP?"ระบุ HTTPS หรือ HTTP ด้วย "+scope+" โดยไม่มี path ข้อมูลเข้าสู่ระบบ query หรือ fragment":"ระบุที่อยู่ Gateway แบบ HTTPS โดยไม่มี path ข้อมูลเข้าสู่ระบบ query หรือ fragment";
  }
  function advancedProxyFields(prefix) {
    let https=false;try{https=new URL(byId(prefix+"-public-url").value.trim()).protocol==="https:";}catch(_){}
    byId(prefix+"-proxy-settings").hidden=!https;
  }
  function transportFields(prefix,allowLANHTTP,allowLocalHTTP=false) {
    const enabled=allowLANHTTP||allowLocalHTTP;
    const scope=allowLANHTTP?(allowLocalHTTP?"IP ภายในหรือ localhost":"IP ภายในที่อนุญาต"):"localhost หรือ loopback";
    document.querySelector('label[for="'+prefix+'-public-url"]').textContent=enabled?"ที่อยู่ Gateway":"ที่อยู่ HTTPS ของ Gateway";
    byId(prefix+"-public-url").placeholder=allowLANHTTP?"http://10.10.8.127:8080":allowLocalHTTP?"http://localhost:8080":"https://gateway.example.org";
    byId(prefix+"-transport-help").textContent=enabled?(prefix==="setup"?"ยืนยันที่อยู่ที่ใช้เปิดหน้านี้สำหรับเข้า Gateway เมื่อใช้ HTTP ข้อมูลเข้าสู่ระบบจะไม่เข้ารหัสระหว่างทาง":"ใช้ HTTP ได้ด้วย "+scope+" ตามนโยบายที่บันทึกไว้ เมื่อใช้ HTTP ข้อมูลเข้าสู่ระบบจะไม่เข้ารหัสระหว่างทาง"):"ใช้ HTTPS ผ่าน reverse proxy ที่คุณดูแล โดยไม่ใส่ /setup/ หรือ /admin/";
    advancedProxyFields(prefix);
  }
  function fillSettingsForm(value){
    for(const [key,id] of Object.entries(settingsFields))byId(id).value=key==="public_url"?(value?.[key]||""):(value?.[key]||[]).join("\n");
    advancedProxyFields("settings");
    ui.settingsDirty=false;ui.settingsDraft=null;byId("settings-review").hidden=true;setError("settings-error","");
  }
  function settingsPayload(){
    const value={};for(const [key,id] of Object.entries(settingsFields))value[key]=key==="public_url"?byId(id).value.trim():byId(id).value.split(/[\n,]+/).map(line=>line.trim()).filter(Boolean);return value;
  }
  async function loadSettings(){
    if(!ui.authenticated||ui.screen!=="settings")return;
    const request=++settingsRequest,epoch=ui.epoch;byId("settings-refresh").disabled=true;
    const results=await Promise.allSettled([api("dashboard"),api("settings")]);
    if(epoch!==ui.epoch||request!==settingsRequest)return;
    byId("settings-refresh").disabled=false;if(ui.screen!=="settings")return;
    if(results[0].status==="fulfilled"){const data=results[0].value;const settings=results[1].status==="fulfilled"?results[1].value:null;const localHTTP=settings?.local_http_enabled===true&&settings?.settings?.public_url?.startsWith("http:");const lanHTTP=settings?.lan_http_enabled===true&&settings?.settings?.public_url?.startsWith("http:");byId("settings-summary").replaceChildren(summary([["การตั้งค่าระบบ",data.setup?.configured?"ตั้งค่าแล้ว":"ยังไม่พร้อม"],["การเข้า Gateway",lanHTTP?"HTTP ภายใน LAN ไม่เข้ารหัส":localHTTP?"HTTP บนเครื่องนี้ ไม่เข้ารหัส":"HTTPS ผ่าน Reverse proxy"],["หน่วยงานเปิดใช้งาน",number(data.hospitals?.enabled)+" แห่ง"],["ใบรับรอง Gateway",data.certificates?.gateway?.subject||"ยังไม่มีข้อมูล"],["บริการออกใบรับรอง",data.certificates?.issuer_status==="not_configured"?"ยังไม่ได้ตั้งค่า ใช้ CSR และนำเข้าใบที่ลงนามแล้ว":data.certificates?.issuer_status],["ข้อมูลล่าสุด",when(data.as_of)]]));}
    if(results[1].status==="fulfilled"){
      const data=results[1].value;ui.settingsLANHTTPEnabled=data.lan_http_enabled===true;ui.settingsLocalHTTPEnabled=data.local_http_enabled===true;transportFields("settings",ui.settingsLANHTTPEnabled,ui.settingsLocalHTTPEnabled);ui.settingsEditable=Boolean(data.editable&&data.settings);ui.settingsBase=data.settings||null;
      byId("settings-form").hidden=!ui.settingsEditable;byId("recovery-export-form").hidden=!ui.settingsEditable;byId("recovery-unavailable").hidden=ui.settingsEditable;
      const message=byId("settings-edit-message");message.textContent=data.message||(ui.settingsEditable?"แก้ไขร่างและตรวจรายการเปลี่ยนแปลงก่อนยืนยันใช้การตั้งค่าใหม่":"Gateway นี้ยังใช้ environment ให้ย้ายเข้าการตั้งค่าถาวรตามคู่มือ WEB_SETUP โดยรักษา key และข้อมูลเดิมก่อนแก้ไขผ่านเว็บ");message.className="notice"+(ui.settingsEditable?"":" warning");
      if(!ui.settingsDirty||!ui.settingsEditable)fillSettingsForm(ui.settingsBase);
      if(!ui.settingsEditable)byId("recovery-export-form").reset();
    }else{setError("settings-error",results[1].reason);notice(results[1].reason.message,"error");}
    if(results[0].status==="rejected")notice(results[0].reason.message,"error");
  }
  byId("settings-refresh").addEventListener("click",loadSettings);
  byId("settings-form").addEventListener("input",()=>{ui.settingsDirty=true;ui.settingsDraft=null;byId("settings-review").hidden=true;setError("settings-error","");});
  byId("settings-public-url").addEventListener("input",()=>advancedProxyFields("settings"));
  byId("settings-reset").addEventListener("click",()=>fillSettingsForm(ui.settingsBase));
  byId("settings-form").addEventListener("submit",event=>{
    event.preventDefault();if(!ui.settingsEditable||ui.settingsSaving||!event.currentTarget.reportValidity())return;setError("settings-error","");
    const draft=settingsPayload();if(!gatewayURLValid(draft.public_url,ui.settingsLANHTTPEnabled,ui.settingsLocalHTTPEnabled)){setError("settings-error",gatewayURLMessage(ui.settingsLANHTTPEnabled,ui.settingsLocalHTTPEnabled));return;}const changes=Object.keys(settingsFields).filter(key=>JSON.stringify(draft[key])!==JSON.stringify(ui.settingsBase?.[key]??(key==="public_url"?"":[])));
    ui.settingsDraft=changes.length?draft:null;const rows=changes.map(key=>{const row=node("div","settings-change");row.append(node("h4","",settingsLabels[key]));const values=node("div","settings-change-values");for(const [label,value] of [["ค่าที่บันทึก",ui.settingsBase?.[key]],["ค่าที่จะใช้",draft[key]]]){const dl=node("dl"),text=Array.isArray(value)?value.join("\n"):value;dl.append(node("dt","",label),node("dd","",text||"ไม่ระบุ"));values.append(dl);}row.append(values);return row;});
    byId("settings-diff").replaceChildren(...rows);if(!changes.length)byId("settings-diff").append(node("p","muted","ไม่มีค่าที่เปลี่ยนจากการตั้งค่าปัจจุบัน"));byId("settings-apply").disabled=!changes.length;byId("settings-review").hidden=false;byId("settings-review-heading").tabIndex=-1;byId("settings-review-heading").focus();
  });
  byId("settings-apply").addEventListener("click",async()=>{
    if(!ui.settingsEditable||ui.settingsSaving||!ui.settingsDraft)return;
    if(JSON.stringify(settingsPayload())!==JSON.stringify(ui.settingsDraft)){ui.settingsDraft=null;byId("settings-review").hidden=true;setError("settings-error","ข้อมูลเปลี่ยนหลังตรวจรายการ กรุณาตรวจรายการเปลี่ยนแปลงอีกครั้ง");return;}
    const epoch=ui.epoch;ui.settingsSaving=true;byId("settings-apply").disabled=true;for(const id of [...Object.values(settingsFields),"settings-reset","settings-review-submit"])byId(id).disabled=true;setError("settings-error","");
    try{const result=await api("settings",{method:"PUT",body:ui.settingsDraft});if(epoch!==ui.epoch)return;if(result.applied){ui.settingsDirty=false;ui.settingsDraft=null;if(result.relogin_required){setAuthenticated(false);notice("บันทึกและใช้การตั้งค่าใหม่แล้ว กรุณาเข้าสู่ระบบอีกครั้ง","success");}else{notice("บันทึกและใช้การตั้งค่าใหม่แล้ว","success");await loadSettings();}}}
    catch(error){if(epoch===ui.epoch)setError("settings-error",error);}
    finally{if(epoch===ui.epoch){ui.settingsSaving=false;byId("settings-apply").disabled=!ui.settingsDraft;for(const id of [...Object.values(settingsFields),"settings-reset","settings-review-submit"])byId(id).disabled=false;}}
  });
  byId("recovery-confirm").addEventListener("input",()=>byId("recovery-confirm").setCustomValidity(""));
  byId("recovery-passphrase").addEventListener("input",()=>{byId("recovery-confirm").setCustomValidity("");byId("recovery-passphrase").setCustomValidity("");});
  byId("recovery-export-form").addEventListener("submit",async event=>{
    event.preventDefault();if(!ui.settingsEditable||ui.recoveryBusy)return;const confirmation=byId("recovery-confirm"),password=byId("recovery-passphrase");password.setCustomValidity([...password.value].length>=16?"":"รหัสผ่านต้องมีอย่างน้อย 16 ตัวอักษร");confirmation.setCustomValidity(confirmation.value===password.value?"":"รหัสผ่านยืนยันไม่ตรงกัน");if(!event.currentTarget.reportValidity())return;
    const epoch=ui.epoch;let passphrase=byId("recovery-passphrase").value;ui.recoveryBusy=true;ui.recoveryController=new AbortController();byId("recovery-export-submit").disabled=true;setError("recovery-export-error","");const status=byId("recovery-export-status");status.hidden=false;status.textContent="กำลังเข้ารหัสชุดกู้คืน…";
    try{
      const response=await fetch("/admin/api/recovery/export",{method:"POST",credentials:"same-origin",redirect:"error",cache:"no-store",headers:{Accept:"application/json","Content-Type":"application/json","X-CSRF-Token":state.csrf},body:JSON.stringify({passphrase}),signal:AbortSignal.any([ui.recoveryController.signal,AbortSignal.timeout(45000)])});
      passphrase="";if(epoch!==ui.epoch||ui.screen!=="settings")return;
      if(!response.ok){let result;try{result=await response.json();}catch(_){}if(response.status===401){setAuthenticated(false);notice("กรุณาเข้าสู่ระบบก่อนดาวน์โหลดชุดกู้คืนอีกครั้ง","error");return;}throw new Error(result?.error?.message||"สร้างชุดกู้คืนไม่สำเร็จ กรุณาลองอีกครั้ง");}
      if(!response.headers.get("Content-Disposition")?.toLowerCase().startsWith("attachment"))throw new Error("ข้อมูลตอบกลับไม่ใช่ไฟล์กู้คืน กรุณาลองอีกครั้ง");
      const blob=await response.blob();if(epoch!==ui.epoch||ui.screen!=="settings")return;
      const url=URL.createObjectURL(blob),link=document.createElement("a");link.href=url;link.download="gateway-recovery-"+new Date().toISOString().slice(0,10)+".json";document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);
      status.textContent="เริ่มดาวน์โหลดชุดกู้คืนที่เข้ารหัสแล้ว เก็บรหัสผ่านแยกจากไฟล์ และสำรองฐานข้อมูลแบบ consistent แยกต่างหาก";
    }catch(error){if(epoch===ui.epoch&&ui.screen==="settings"){status.hidden=true;setError("recovery-export-error",error.name==="AbortError"?new Error("ยกเลิกการดาวน์โหลดแล้ว"):error);}}
    finally{passphrase="";if(epoch===ui.epoch){ui.recoveryBusy=false;ui.recoveryController=null;byId("recovery-export-submit").disabled=false;byId("recovery-export-form").reset();}}
  });
  function empty(id,message){byId(id).replaceChildren(node("p","muted",message));}
  async function loadUpdateStatus(){
    const epoch=ui.epoch;
    try{const data=await api("updates/status");if(epoch!==ui.epoch)return;ui.updateConfigured=Boolean(data.configured);const box=byId("updates-status");box.className="notice "+(data.configured?"":"warning");box.textContent=data.configured?"ระบบรับรุ่นที่ลงนามแล้ว · Updater ที่ลงทะเบียน "+number(data.agents_total)+" แห่ง · รอคิว "+number(data.counts?.queued ?? 0)+" งาน":"ยังไม่ได้ตั้งค่า public key สำหรับตรวจ release และ repository ที่อนุญาตบน Gateway จึงยังส่งงานอัปเดตไม่ได้";updateSelectionState();byId("release-form").querySelector("button[type=submit]").disabled=!ui.updateConfigured;}
    catch(error){if(epoch===ui.epoch){ui.updateConfigured=false;byId("updates-status").textContent=error.message;updateSelectionState();}}
  }
  function renderReleases(items){const select=byId("update-release"),value=select.value;select.replaceChildren(node("option","",items.length?"เลือกรุ่น API":"ยังไม่มีรุ่นที่ลงทะเบียน"));select.firstChild.value="";for(const r of items){const o=node("option","",r.version+" · "+r.architecture);o.value=r.id;select.append(o);}if(items.some(r=>r.id===value))select.value=value;updateSelectionState();}
  const jobLabels={queued:"รอรับงาน",running:"กำลังดำเนินการ",succeeded:"สำเร็จ",failed:"ไม่สำเร็จ",blocked:"หยุดรอตรวจสอบ",waiting:"รอเครื่องออนไลน์",leased:"Updater รับงานแล้ว",cancelled:"ยกเลิก",rolled_back:"ย้อนกลับแล้ว"};
  function renderJobs(items){const rows=items.map(j=>{const row=node("div","job-row"),copy=node("div");copy.append(node("strong","",(j.hospital_name||j.hospital_code||"หน่วยงาน")+" · "+(j.release_id||"")),node("p","",j.message||j.stage||"รอสถานะจาก Updater"),node("small","",when(j.updated_at||j.created_at)));if(j.actual_image)copy.append(node("p","","รุ่นที่รายงาน: "+j.actual_image));row.append(copy,badge(jobLabels[j.status]||j.status,j.status==="succeeded"?"success":j.status==="failed"||j.status==="blocked"?"warning":"neutral"));return row;});byId("update-jobs").replaceChildren(...rows);if(!rows.length)empty("update-jobs","ยังไม่มีงานอัปเดต เลือกรุ่นและหน่วยงานเพื่อสร้างงานแรก");}
  function renderAgents(items){const rows=items.map(a=>{const row=node("div","agent-row"),copy=node("div");copy.append(node("strong","",a.hospital_name||a.hospital_code),node("p","",(a.architecture||"ยังไม่ทราบสถาปัตยกรรม")+" · Updater "+(a.updater_version||"ยังไม่มีรุ่น")),node("small","","เชื่อมต่อล่าสุด "+when(a.last_seen)));if(a.actual_image)copy.append(node("p","",a.actual_image));row.append(copy,badge(a.status==="online"?"เชื่อมต่อแล้ว":a.status==="offline"?"ออฟไลน์":a.status||"ยังไม่เชื่อมต่อ",a.status==="online"?"success":"neutral"));return row;});byId("updater-agents").replaceChildren(...rows);if(!rows.length)empty("updater-agents","ยังไม่มี Updater รายงานตัว ติดตั้งและลงทะเบียน Updater ที่หน่วยงานก่อน");}
  function renderUpdateHospitals(items){
    const rows=items.map(h=>{const row=node("div","selection-item"),label=node("label"),checkbox=document.createElement("input");checkbox.type="checkbox";checkbox.checked=ui.selected.has(h.id);checkbox.addEventListener("change",()=>{checkbox.checked?ui.selected.add(h.id):ui.selected.delete(h.id);updateSelectionState();});const copy=node("span");copy.append(node("strong","",h.name),node("small","",h.code+" · "+(h.province||"ไม่ระบุจังหวัด")));label.append(checkbox,copy);const token=node("button","button subtle compact","สร้าง Updater token");token.type="button";token.addEventListener("click",()=>createUpdaterToken(h,token));row.append(label,token);return row;});
    byId("update-hospitals").replaceChildren(...rows);if(!rows.length)empty("update-hospitals","ไม่พบหน่วยงานตามคำค้นหา");updateSelectionState();
  }
  function updateSelectionState(){const all=document.querySelector('[name="update-scope"]:checked').value==="all";byId("update-targets").hidden=all;byId("update-selected-count").textContent=all?"ระบบเลือกทุกหน่วยงานที่เข้ากับรุ่นและผ่านเงื่อนไขที่เซิร์ฟเวอร์":"เลือกแล้ว "+number(ui.selected.size)+" หน่วยงาน รวมรายการที่ซ่อนด้วยคำค้นหา";byId("update-submit").disabled=!ui.updateConfigured||!byId("update-release").value||(!all&&!ui.selected.size);}
  document.querySelectorAll('[name="update-scope"]').forEach(el=>el.addEventListener("change",()=>{updateSelectionState();if(el.value==="selected"&&!pages.get("updateHospitals").loaded)loadPage("updateHospitals",true);}));byId("update-release").addEventListener("change",updateSelectionState);
  byId("update-release").addEventListener("scroll",()=>{const el=byId("update-release"),p=pages.get("releases");if(el.scrollHeight-el.scrollTop-el.clientHeight<80&&!p.error)loadPage("releases");});
  byId("update-search").addEventListener("input",()=>{clearTimeout(updateSearchTimer);updateSearchTimer=setTimeout(()=>loadPage("updateHospitals",true),300);});
  byId("update-hospitals").addEventListener("scroll",()=>{const el=byId("update-hospitals"),p=pages.get("updateHospitals");if(el.scrollHeight-el.scrollTop-el.clientHeight<120&&!p.error)loadPage("updateHospitals");});
  byId("updates-refresh").addEventListener("click",()=>showScreen("updates",{refresh:true}));
  byId("update-form").addEventListener("submit",async event=>{
    event.preventDefault();if(byId("update-submit").disabled)return;setError("update-error","");
    const scope=document.querySelector('[name="update-scope"]:checked').value,release=pages.get("releases").items.find(r=>r.id===byId("update-release").value);
    const dialog=byId("update-confirm-dialog");byId("update-confirm-summary").textContent="รุ่น "+(release?.version||"")+" · "+(scope==="all"?"ทุกหน่วยงานที่เข้ากันได้":number(ui.selected.size)+" หน่วยงานที่เลือก")+" · พร้อมกันไม่เกิน "+byId("update-parallel").value+" แห่ง";dialog.returnValue="cancel";
    const confirmed=new Promise(resolve=>dialog.addEventListener("close",()=>resolve(dialog.returnValue==="confirm"),{once:true}));dialog.showModal();if(!await confirmed)return;
    const button=byId("update-submit");button.disabled=true;
    try{const result=await api("updates/jobs",{method:"POST",body:{release_id:byId("update-release").value,scope,hospital_ids:scope==="selected"?[...ui.selected]:[],max_parallel:Number(byId("update-parallel").value)}});notice("จัดคิวอัปเดต "+number(result.queued ?? result.total)+" หน่วยงานแล้ว ติดตามผลจากรายการงาน","success");await loadPage("jobs",true);}
    catch(error){setError("update-error",error);}finally{updateSelectionState();}
  });
  byId("release-form").addEventListener("submit",async event=>{event.preventDefault();setError("release-error","");const button=event.currentTarget.querySelector("button[type=submit]");if(button.disabled)return;button.disabled=true;try{const manifest=JSON.parse(byId("release-manifest").value);if(!manifest||typeof manifest!=="object"||Array.isArray(manifest))throw new Error("Manifest ต้องเป็น JSON object");await api("updates/releases",{method:"POST",body:{...manifest,signature:byId("release-signature").value.trim()}});notice("ตรวจลายเซ็นและลงทะเบียนรุ่นแล้ว","success");event.target.reset();await loadPage("releases",true);}catch(error){setError("release-error",error);}finally{button.disabled=!ui.updateConfigured;}});
  async function createUpdaterToken(hospital,button){
    if(!window.confirm("สร้าง token ใหม่สำหรับ "+hospital.name+"? Token เดิมของ Updater แห่งนี้จะใช้ไม่ได้"))return;
    button.disabled=true;try{const result=await api("hospitals/"+encodeURIComponent(hospital.id)+"/updater-token",{method:"POST",body:{}});byId("updater-token-value").value=result.token||"";byId("token-dialog").showModal();}catch(error){notice(error.message,"error");}finally{button.disabled=false;}
  }
  async function copyText(id){const text=byId(id).value;try{await navigator.clipboard.writeText(text);notice("คัดลอกแล้ว","success");}catch(_){byId(id).focus();byId(id).select();notice("เลือกข้อความแล้ว กดคัดลอกด้วยแป้นพิมพ์");}}
  const signingCommandBase=byId("setup-ca-sign-command").value;
  function updateClientValidity(){
    const input=byId("setup-ca-validity-days"),raw=input.value.trim(),days=/^\d+$/.test(raw)?Number(raw):NaN;
    const valid=Number.isInteger(days)&&days>=1&&days<=365;
    byId("setup-ca-sign-command").value=valid?signingCommandBase+" \\\n  "+days:"";
    const error=byId("setup-ca-validity-error");error.textContent=valid?"":"ระบุจำนวนเต็ม 1–365 วัน";error.hidden=valid;input.setAttribute("aria-invalid",valid?"false":"true");
    document.querySelector('[data-ca-command-copy="setup-ca-sign-command"]').disabled=!valid;
    document.querySelectorAll("[data-ca-validity-preset]").forEach(button=>button.setAttribute("aria-pressed",valid&&Number(button.dataset.caValidityPreset)===days?"true":"false"));
    input.closest(".ca-signing-guide").querySelector('[role="status"]').textContent="";
  }
  byId("setup-ca-validity-days").addEventListener("input",updateClientValidity);
  byId("setup-ca-validity-days").addEventListener("keydown",event=>{if(event.key==="Enter")event.preventDefault();});
  document.querySelectorAll("[data-ca-validity-preset]").forEach(button=>button.addEventListener("click",()=>{byId("setup-ca-validity-days").value=button.dataset.caValidityPreset;updateClientValidity();}));
  updateClientValidity();
  document.querySelectorAll("[data-ca-command-copy]").forEach(button=>button.addEventListener("click",async()=>{
    if(button.disabled)return;
    const command=byId(button.dataset.caCommandCopy),feedback=button.closest(".ca-signing-guide").querySelector('[role="status"]');
    feedback.textContent="";
    try{await navigator.clipboard.writeText(command.value);feedback.textContent="คัดลอกคำสั่งแล้ว นำไปวางใน Terminal บน Mac";}
    catch(_){command.focus();command.select();feedback.textContent="เลือกคำสั่งแล้ว กด ⌘C บน Mac หรือ Ctrl+C เพื่อคัดลอก";}
  }));
  function downloadPublicCSR(id){
    const text=byId(id).value;
    if(text.length>64*1024 || !/^-----BEGIN CERTIFICATE REQUEST-----\r?\n(?:[A-Za-z0-9+/=]+\r?\n)+-----END CERTIFICATE REQUEST-----\s*$/.test(text)){
      notice("ยังไม่มี CSR สำหรับดาวน์โหลด กรุณาสร้างคำขอ CSR ก่อน","error");return;
    }
    const url=URL.createObjectURL(new Blob([text],{type:"application/pkcs10"}));
    const link=document.createElement("a");link.href=url;link.download="gateway-client.csr.pem";
    document.body.append(link);link.click();link.remove();
    setTimeout(()=>URL.revokeObjectURL(url),1000);
    notice("ส่งไฟล์ gateway-client.csr.pem ให้เบราว์เซอร์แล้ว ส่งไฟล์นี้ให้ผู้ดูแล CA ลงนามได้","success");
  }
  byId("token-copy").addEventListener("click",()=>copyText("updater-token-value"));
  byId("token-close").addEventListener("click",()=>byId("token-dialog").close());
  byId("token-dialog").addEventListener("close",()=>byId("updater-token-value").value="");
  async function loadCertificates(){
    const request=++certificateRequest,epoch=ui.epoch;
    try{const data=await api("certificates");if(request!==certificateRequest||epoch!==ui.epoch)return;
      const panel=node("section","panel certificate-state");panel.append(node("h2","","Gateway client certificate"));
      const cert=data.gateway;
      if(cert){const statuses={valid:"ใช้งานได้",expiring:"ใกล้หมดอายุ",expired:"หมดอายุ",not_yet_valid:"ยังไม่ถึงวันเริ่มใช้"};const dl=node("dl","summary-list");dl.append(summary([["ชื่อ identity",cert.subject],["Serial",cert.serial],["ใช้ได้ตั้งแต่",when(cert.not_before)],["หมดอายุ",when(cert.not_after)],["อายุคงเหลือ",number(cert.days_remaining)+" วัน"],["สถานะ",statuses[cert.status]||"ยังไม่ทราบสถานะ"]]));panel.append(dl);if(cert.fingerprint)panel.append(node("p","fingerprint","Fingerprint: "+cert.fingerprint));}
      else panel.append(node("p","muted","ยังไม่มีข้อมูลใบรับรอง Gateway ที่พร้อมใช้งาน"));
      const issuer=node("p","notice warning",data.issuer?.message||"ยังไม่ได้ตั้งค่าบริการออกใบ ใช้ CSR ให้ผู้ดูแล CA ลงนาม แล้วนำเข้าใบผ่านหน้านี้");panel.append(issuer);byId("certificate-inventory").replaceChildren(panel);
      byId("certificate-csr").disabled=!data.capabilities?.csr;byId("certificate-import").disabled=!data.capabilities?.import;
      if(!data.capabilities?.csr || !data.capabilities?.import){const explanation=node("p","notice","Gateway นี้ยังใช้การตั้งค่าจาก environment จึงยังสร้าง CSR หรือนำเข้าใบผ่านเว็บไม่ได้ ให้ย้ายเข้าการตั้งค่าถาวรตามคู่มือ WEB_SETUP ก่อน โดยรักษา key และข้อมูลเดิม");explanation.id="certificate-capability-reason";panel.append(explanation);for(const id of ["certificate-csr","certificate-import"])byId(id).setAttribute("aria-describedby",explanation.id);}else{for(const id of ["certificate-csr","certificate-import"])byId(id).removeAttribute("aria-describedby");}
      byId("csr-result").hidden=!data.pending_request?.csr_pem;byId("csr-pem").value=data.pending_request?.csr_pem||"";
    }catch(error){if(request===certificateRequest&&epoch===ui.epoch)notice(error.message,"error");}
  }
  byId("certificates-refresh").addEventListener("click",loadCertificates);
  byId("certificate-csr").addEventListener("click",async()=>{const button=byId("certificate-csr");button.disabled=true;setError("certificate-error","");try{const result=await api("certificates/csr",{method:"POST",body:{}});byId("csr-pem").value=result.csr_pem||"";byId("csr-result").hidden=false;notice("สร้างคำขอ CSR แล้ว ส่งเฉพาะ CSR ให้ CA ลงนาม","success");}catch(error){setError("certificate-error",error);}finally{button.disabled=false;}});
  byId("csr-copy").addEventListener("click",()=>copyText("csr-pem"));
  byId("certificate-import-form").addEventListener("submit",async event=>{event.preventDefault();const button=byId("certificate-import");if(button.disabled)return;button.disabled=true;setError("certificate-import-error","");try{await api("certificates/import",{method:"POST",body:{certificate_pem:byId("certificate-chain").value}});event.target.reset();notice("นำเข้าและเปิดใช้ใบรับรองแล้ว กรุณาเข้าสู่ระบบใหม่หาก session ถูกเปลี่ยน","success");await loadCertificates();}catch(error){setError("certificate-import-error",error);}finally{button.disabled=false;}});
  byId("attachments-more").addEventListener("click",()=>loadAttachments(byId("attachments-more").dataset.retry==="reset"));
  const attachmentObserver="IntersectionObserver" in window?new IntersectionObserver(entries=>{if(entries.some(e=>e.isIntersecting)&&ui.screen==="hospitals"&&!byId("attachments-panel").hidden&&state.attachmentCursor&&!byId("attachments-more").dataset.retry)loadAttachments(false);},{rootMargin:"100px"}):null;
  attachmentObserver?.observe(byId("attachments-sentinel"));
  document.querySelectorAll("[data-pem-target]").forEach(input=>input.addEventListener("change",async()=>{
    const file=input.files?.[0];if(!file)return;
    if(file.size>256*1024){notice("ไฟล์ PEM ต้องไม่เกิน 256 KB","error");input.value="";return;}
    try{byId(input.dataset.pemTarget).value=await file.text();byId(input.dataset.pemTarget).dispatchEvent(new Event("input",{bubbles:true}));}catch(_){notice("อ่านไฟล์ไม่สำเร็จ กรุณาเลือกไฟล์อีกครั้ง","error");}finally{input.value="";}
  }));
  async function setupAPI(path,body) {
    const response=await fetch("/setup/api/"+path,{method:body===undefined?"GET":"POST",credentials:"same-origin",cache:"no-store",redirect:"error",headers:{Accept:"application/json",...(body===undefined?{}:{"Content-Type":"application/json","X-CSRF-Token":ui.setupCSRF})},...(body===undefined?{}:{body:JSON.stringify(body)}),signal:AbortSignal.timeout(30000)});
    let result;try{result=await response.json();}catch(_){throw new Error("ข้อมูลตอบกลับไม่ถูกต้อง กรุณาโหลดหน้าใหม่");}
    if(!response.ok){if(result.error?.code==="SETUP_DISABLED")disableSetup();else if(response.status===401 && path!=="session"){byId("setup-workspace").hidden=true;byId("setup-unlock").hidden=false;ui.setupCSRF="";}throw new Error(result.error?.message||"ตั้งค่าไม่สำเร็จ กรุณาตรวจข้อมูลแล้วลองใหม่");}
    return result.data;
  }
  function disableSetup(){ui.setupEnabled=false;ui.setupValid=false;ui.setupCSRF="";byId("setup-code").value="";byId("setup-activate").disabled=true;byId("setup-unlock").hidden=true;byId("setup-workspace").hidden=true;byId("setup-reason").hidden=true;byId("system-status").textContent="ปิดโหมดตั้งค่า";}
  function consumeSessionMetadata(data){
    if(data.setup_enabled===false){disableSetup();notice("ปิดโหมดตั้งค่า เปิด SETUP=true บนเครื่อง Gateway เพื่อเริ่มตั้งค่า","error");return false;}
    ui.setupEnabled=true;ui.setupLANHTTPEnabled=data.lan_http_enabled===true;ui.setupLocalHTTPEnabled=data.local_http_enabled===true;
    transportFields("setup",ui.setupLANHTTPEnabled,ui.setupLocalHTTPEnabled);
    byId("setup-introduction").textContent=ui.setupLANHTTPEnabled||ui.setupLocalHTTPEnabled?"ตรวจยืนยันที่อยู่ที่เปิดใช้งาน ตั้งบัญชีผู้ดูแล และเตรียมใบรับรองเชื่อมต่อหน่วยงาน":"ใช้ HTTPS เดิม และจัดเตรียมใบรับรองสำหรับเชื่อมต่อหน่วยงาน";
    if(!ui.setupURLTouched&&!byId("setup-public-url").value&&typeof data.public_url==="string")byId("setup-public-url").value=data.public_url;
    advancedProxyFields("setup");return true;
  }
  function setupPayload(){const list=id=>byId(id).value.split(/[\n,]+/).map(v=>v.trim()).filter(Boolean);return {admin_username:byId("setup-username").value.trim(),admin_password:byId("setup-password").value,public_url:byId("setup-public-url").value.trim(),trusted_proxy_cidrs:list("setup-proxies"),approved_upstreams:list("setup-upstreams"),private_upstream_cidrs:list("setup-private-cidrs"),client_certificate_pem:byId("setup-cert").value,client_private_key_pem:byId("setup-key").value,hospital_ca_pem:byId("setup-ca").value,...(byId("setup-encryption-key").value?{encryption_key:byId("setup-encryption-key").value}:{})};}
  function setupStep(step){ui.setupStep=step;document.querySelectorAll("[data-setup-panel]").forEach(el=>el.hidden=Number(el.dataset.setupPanel)!==step);document.querySelectorAll("[data-setup-step]").forEach(el=>{el.classList.toggle("active",Number(el.dataset.setupStep)===step);el.setAttribute("aria-current",Number(el.dataset.setupStep)===step?"step":"false");});byId("setup-back").hidden=step===0;byId("setup-next").hidden=step===3;byId("setup-activate").hidden=step!==3;setError("setup-error","");}
  function checkCurrentStep(){if(ui.setupStep===1&&!gatewayURLValid(byId("setup-public-url").value.trim(),ui.setupLANHTTPEnabled,ui.setupLocalHTTPEnabled)){setError("setup-error",gatewayURLMessage(ui.setupLANHTTPEnabled,ui.setupLocalHTTPEnabled));return false;}for(const field of document.querySelectorAll('[data-setup-panel="'+ui.setupStep+'"] input:not([data-ca-guide-control]),[data-setup-panel="'+ui.setupStep+'"] textarea')){if(!field.reportValidity())return false;}return true;}
  byId("setup-next").addEventListener("click",()=>{if(checkCurrentStep())setupStep(Math.min(3,ui.setupStep+1));});
  byId("setup-back").addEventListener("click",()=>setupStep(Math.max(0,ui.setupStep-1)));
  document.querySelectorAll("[data-setup-step]").forEach(button=>button.addEventListener("click",()=>{const step=Number(button.dataset.setupStep);if(step<ui.setupStep || checkCurrentStep())setupStep(step);}));
  byId("setup-form").addEventListener("input",event=>{if(event.target.closest?.("[data-ca-guide-control]"))return;ui.setupValid=false;byId("setup-activate").disabled=true;});
  byId("setup-public-url").addEventListener("input",()=>{ui.setupURLTouched=true;advancedProxyFields("setup");});
  byId("setup-code-form").addEventListener("submit",async event=>{event.preventDefault();if(!ui.setupEnabled)return;const button=byId("setup-unlock-submit");button.disabled=true;notice("");try{const data=await setupAPI("session",{code:byId("setup-code").value.trim()});if(!consumeSessionMetadata(data))return;ui.setupCSRF=data.csrf_token;byId("setup-code").value="";byId("setup-unlock").hidden=true;byId("setup-workspace").hidden=false;setupStep(0);}catch(error){notice(error.message,"error");}finally{button.disabled=false;}});
  byId("setup-csr-copy").addEventListener("click",()=>copyText("setup-csr-pem"));
  byId("setup-csr-download").addEventListener("click",()=>downloadPublicCSR("setup-csr-pem"));
  byId("setup-csr-create").addEventListener("click",async()=>{const button=byId("setup-csr-create");button.disabled=true;setError("setup-error","");try{const result=await setupAPI("csr",{});byId("setup-csr-pem").value=result.csr_pem||"";byId("setup-csr-result").hidden=false;byId("setup-key").value="";ui.setupValid=false;byId("setup-activate").disabled=true;}catch(error){setError("setup-error",error);}finally{button.disabled=false;}});
  byId("setup-validate").addEventListener("click",async()=>{if(!gatewayURLValid(byId("setup-public-url").value.trim(),ui.setupLANHTTPEnabled,ui.setupLocalHTTPEnabled)){setError("setup-error",gatewayURLMessage(ui.setupLANHTTPEnabled,ui.setupLocalHTTPEnabled));return;}const button=byId("setup-validate");button.disabled=true;setError("setup-error","");ui.setupValid=false;byId("setup-activate").disabled=true;try{const data=await setupAPI("validate",setupPayload());byId("setup-checks").replaceChildren(...(data.checks||[]).map(check=>{const li=node("li"),copy=node("div");li.dataset.check=check.status;const names={admin:"บัญชีผู้ดูแล",https:"การเข้าใช้งาน Gateway",mtls:"ใบรับรอง mTLS",hospital_connectivity:"การเชื่อมต่อโรงพยาบาล"};copy.append(node("strong","",names[check.name]||check.name),node("p","",check.message||check.status));li.append(icon(check.status==="ready"?"check":"clock"),copy);return li;}));ui.setupValid=Boolean(data.valid);byId("setup-activate").disabled=!ui.setupValid;byId("setup-validation-note").textContent=data.valid?"ข้อมูลผ่านการตรวจ พร้อมบันทึกและเปิดใช้งาน":"ข้อมูลยังไม่พร้อม กรุณาตรวจรายการด้านบน";}catch(error){setError("setup-error",error);}finally{button.disabled=false;}});
  byId("setup-form").addEventListener("submit",async event=>{event.preventDefault();if(!ui.setupValid)return;const button=byId("setup-activate");button.disabled=true;setError("setup-error","");try{const result=await setupAPI("activate",setupPayload());byId("setup-form").reset();ui.setupCSRF="";ui.setupValid=false;location.assign(result.redirect==="/admin/"?result.redirect:"/admin/");}catch(error){setError("setup-error",error);button.disabled=false;}});
  async function startSetup(){
    byId("initial-loading").hidden=true;byId("setup-view").hidden=false;byId("login-view").hidden=true;byId("admin-view").hidden=true;byId("system-status").textContent="ตั้งค่าครั้งแรก";
    try{const data=await setupAPI("status");if(data.state==="active"){location.replace("/admin/");return;}if(data.setup_enabled===false){disableSetup();notice("ปิดโหมดตั้งค่า เปิด SETUP=true บนเครื่อง Gateway เพื่อเริ่มตั้งค่า","error");return;}byId("setup-code-command").textContent=data.code_command||byId("setup-code-command").textContent;byId("setup-reason").hidden=!data.reason;byId("setup-reason").textContent=data.reason||"";byId("setup-recovery-field").hidden=!data.existing_data;byId("setup-encryption-key").required=Boolean(data.existing_data);if(data.authenticated){if(!consumeSessionMetadata(data))return;ui.setupCSRF=data.csrf_token;byId("setup-unlock").hidden=true;byId("setup-workspace").hidden=false;}setupStep(0);}
    catch(error){notice(error.message,"error");}
  }
  window.addEventListener("pagehide",()=>{observer?.disconnect();attachmentObserver?.disconnect();for(const p of pages.values())p.controller?.abort();});
  if(location.pathname.startsWith("/setup"))startSetup();else window.dispatchEvent(new Event("gateway-ui-ready"));
})();
