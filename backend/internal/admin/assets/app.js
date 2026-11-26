"use strict";

const byId = (id) => document.getElementById(id);
const state = { csrf: "", hospitals: [], busy: new Set(), suspend: null, editing: null, attachmentHospital: null, attachments: [], attachmentsLoaded: false, attachmentBusy: false, attachmentLoading: false, attachmentRequest: 0, attachmentDelete: null, attachmentCursor: null, attachmentTotal: 0 };
const dateFormat = new Intl.DateTimeFormat("th-TH", { day: "numeric", month: "short", year: "numeric", timeZone: "Asia/Bangkok" });
const clockFormat = new Intl.DateTimeFormat("th-TH", { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: "Asia/Bangkok" });
const dayFormat = new Intl.DateTimeFormat("th-TH", { dateStyle: "medium", timeZone: "UTC" });
const attachmentCategories = { document: "เอกสารทั่วไป", slip: "สลิป", bill: "บิล / ใบเสร็จ", contract: "สัญญา" };

function notice(message, type = "info") {
  const box = byId("notice");
  box.textContent = message;
  box.className = `notice ${type}`;
  box.hidden = !message;
}

function setAuthenticated(authenticated) {
  byId("initial-loading").hidden = true;
  byId("login-view").hidden = authenticated;
  byId("admin-view").hidden = !authenticated;
  byId("logout").hidden = !authenticated;
  window.gatewayUI?.authenticated(authenticated);
  if (!authenticated) {
    state.csrf = "";
    state.hospitals = [];
    byId("hospital-rows").replaceChildren();
    byId("audit-events").replaceChildren();
    byId("add-form").reset();
    resetEditor();
    setAddOpen(false);
    closeAttachments(false);
    if (byId("suspend-dialog").open) byId("suspend-dialog").close();
    if (byId("edit-dialog").open) byId("edit-dialog").close("cancel");
  }
}

async function api(path, { method = "GET", body, signal } = {}) {
  const multipart = body instanceof FormData;
  let response;
  try {
    response = await fetch(`/admin/api/${path}`, {
      method,
      credentials: "same-origin",
      redirect: "error",
      cache: "no-store",
      headers: {
        Accept: "application/json",
        ...(body === undefined || multipart ? {} : { "Content-Type": "application/json" }),
        ...(method === "GET" || path === "login" ? {} : { "X-CSRF-Token": state.csrf }),
      },
      ...(body === undefined ? {} : { body: multipart ? body : JSON.stringify(body) }),
      signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(multipart ? 90000 : 25000)]) : AbortSignal.timeout(multipart ? 90000 : 25000),
    });
  } catch (_) {
    throw new Error("เชื่อมต่อ Gateway ไม่ได้ กรุณาลองอีกครั้ง");
  }
  let result;
  try {
    result = await response.json();
  } catch (_) {
    throw new Error("ข้อมูลตอบกลับไม่ถูกต้อง กรุณาลองอีกครั้ง");
  }
  if (!response.ok) {
    if (response.status === 401 && path !== "login") setAuthenticated(false);
    const error = new Error(result.error?.message || "ดำเนินการไม่สำเร็จ กรุณาลองอีกครั้ง");
    error.status = response.status;
    throw error;
  }
  return result.data;
}

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
}

function badge(text, style) {
  return node("span", `badge ${style}`, text);
}

function when(timestamp) {
  if (!timestamp) return "ยังไม่มีข้อมูล";
  const value = new Date(timestamp * 1000);
  return `${dateFormat.format(value)} เวลา ${clockFormat.format(value)} น.`;
}

function day(value) {
  return value ? dayFormat.format(new Date(`${value}T00:00:00Z`)) : "ไม่ระบุ";
}

function actionButton(label, hospital, action, { style = "secondary", disabled = false } = {}) {
  const button = node("button", `button compact ${style}`, label);
  button.type = "button";
  button.disabled = disabled || state.busy.has(hospital.id) || (action === "attachments" && state.attachmentBusy);
  button.dataset.action = action;
  button.dataset.hospital = hospital.id;
  button.setAttribute("aria-label", `${label} ${hospital.name} รหัส ${hospital.code}`);
  button.addEventListener("click", () => {
    if (action === "suspend") openSuspend(hospital);
    else if (action === "edit") openEditor(hospital);
    else if (action === "attachments") openAttachments(hospital);
    else changeHospital(hospital, action).catch(() => {});
  });
  return button;
}

function hospitalUsageCell(usage) {
  const cell = node("td", "hospital-usage");
  cell.dataset.label = "การใช้แอป 30 วัน";
  const known = (value) => typeof value === "number" && Number.isFinite(value) && value >= 0;
  const format = (value) => new Intl.NumberFormat("th-TH", { maximumFractionDigits: 1 }).format(value);
  const fresh = usage?.status === "ready" || usage?.status === "partial";
  const complete = fresh && known(usage?.adoption_30d) && known(usage?.active_30d) && known(usage?.personnel);
  if (complete) {
    cell.append(node("strong", "usage-value", format(usage.adoption_30d) + "%"));
    cell.append(node("small", "usage-people", format(usage.active_30d) + " / " + format(usage.personnel) + " คน"));
    if (usage.status === "partial") cell.append(node("small", "usage-note", "ข้อมูลบางส่วน"));
  } else {
    const labels = { partial: "ข้อมูลยังไม่ครบ", stale: "ข้อมูลล่าช้า", unsupported: "API ยังไม่รองรับข้อมูลการใช้แอป", not_reported: "ยังไม่มีรายงาน", unavailable: "ยังรับข้อมูลไม่ได้" };
    cell.append(node("strong", "usage-value usage-unknown", "—"), node("small", "usage-note", labels[usage?.status] || "ยังไม่มีข้อมูล"));
  }
  if (fresh && (known(usage?.android_30d) || known(usage?.ios_30d))) {
    cell.append(node("small", "usage-installations", "การติดตั้ง: Android " + (known(usage.android_30d) ? format(usage.android_30d) : "—") + " · iOS " + (known(usage.ios_30d) ? format(usage.ios_30d) : "—") + " เครื่อง"));
  }
  if (usage?.as_of) cell.title = "ข้อมูลล่าสุด " + when(usage.as_of) + " · จำนวนคนและการติดตั้งเป็นคนละตัวชี้วัด";
  return cell;
}

function renderHospitals() {
  const selected = state.hospitals;
  const rows = selected.map((hospital) => {
    const row = node("tr");
    const identity = node("td");
    identity.append(node("div", "hospital-name", hospital.name));
    const meta = node("span", "hospital-meta");
    meta.append(node("span", "hospital-code", hospital.code), document.createTextNode(` · ${hospital.province || "ไม่ระบุจังหวัด"}`));
    identity.append(meta);
    identity.append(node("span", "hospital-dates", `เริ่มใช้ ${day(hospital.start_date)} · หมดอายุ ${day(hospital.expiry_date)}`));
    const approval = node("td");
    approval.dataset.label = "การอนุมัติ";
    const approved = hospital.approval_status === "approved";
    approval.append(badge(approved ? "อนุมัติแล้ว" : "รออนุมัติ", approved ? "neutral" : "warning"));
    const access = node("td");
    access.dataset.label = "สิทธิ์ใช้งานแอป";
    access.append(badge(!approved ? "ยังไม่เปิดใช้งาน" : hospital.enabled ? "เปิดใช้งาน" : "ระงับการใช้งาน", !approved ? "neutral" : hospital.enabled ? "success" : "danger"));
    const connection = node("td");
    connection.dataset.label = "การเชื่อมต่อ";
    const online = hospital.connection_status === "online";
    connection.append(badge(online ? "เชื่อมต่อได้" : hospital.connection_status === "offline" ? "ติดต่อไม่ได้" : "ยังไม่ได้ทดสอบ", online ? "success" : hospital.connection_status === "offline" ? "warning" : "neutral"));
    if (hospital.checked_at) connection.append(node("span", "checked-at", when(hospital.checked_at)));
    const management = node("td");
    const actions = node("div", "row-actions");
    actions.append(actionButton("แก้ไข", hospital, "edit", { style: "subtle" }));
    actions.append(actionButton("เอกสารแนบ", hospital, "attachments", { style: "subtle" }));
    actions.append(actionButton(state.busy.has(hospital.id) ? "กำลังดำเนินการ…" : "ทดสอบ", hospital, "test"));
    if (!approved) actions.append(actionButton("อนุมัติและเปิดใช้", hospital, "approve", { style: "primary", disabled: !online }));
    else if (hospital.enabled) actions.append(actionButton("ระงับ", hospital, "suspend", { style: "secondary" }));
    else actions.append(actionButton("เปิดใช้งาน", hospital, "enable", { style: "primary", disabled: !online }));
    management.append(actions);
    row.append(identity, hospitalUsageCell(hospital.usage), approval, access, connection, management);
    return row;
  });
  byId("hospital-rows").replaceChildren(...rows);
  byId("empty-state").hidden = selected.length !== 0;
  byId("empty-heading").textContent = (byId("search").value.trim() || byId("filter").value !== "all") ? "ไม่พบโรงพยาบาลที่ค้นหา" : "ยังไม่มีโรงพยาบาล";
  byId("empty-description").textContent = (byId("search").value.trim() || byId("filter").value !== "all") ? "ลองเปลี่ยนคำค้นหาหรือเลือกสถานะอื่น" : "เพิ่มโรงพยาบาล แล้วทดสอบการเชื่อมต่อเพื่อเริ่มอนุมัติหน่วยงาน";
  byId("result-count").textContent = `แสดง ${selected.length} จาก ${state.hospitalTotal ?? state.hospitals.length} โรงพยาบาล`;
}

function renderAudit(events) {
  const labels = { create: "เพิ่มหน่วยงาน", created: "เพิ่มหน่วยงาน", update: "แก้ไขข้อมูลหน่วยงาน", test: "ทดสอบการเชื่อมต่อ", approve: "อนุมัติและเปิดใช้งาน", suspend: "ระงับการใช้งานบนมือถือ", enable: "เปิดใช้งานอีกครั้ง", upload_attachment: "แนบเอกสาร", delete_attachment: "ลบเอกสารแนบ" };
  byId("audit-events").replaceChildren(...events.map((event) => {
    const item = node("li", "audit-event");
    const time = node("time", "", when(event.created_at));
    if (event.created_at) time.dateTime = new Date(event.created_at * 1000).toISOString();
    const details = node("div", "", `${labels[event.action] || event.action} · ${event.hospital_code || event.hospital_id || "ส่วนกลาง"}`);
    const reasons = { "configuration changed; approval reset": "เปลี่ยนข้อมูลและตั้งสถานะรออนุมัติใหม่", "usage dates changed": "แก้ไขวันเริ่มใช้งานหรือวันหมดอายุ" };
    if (event.reason) details.append(node("span", "audit-reason", reasons[event.reason] || event.reason));
    item.append(time, details);
    return item;
  }));
  byId("audit-empty").hidden = events.length !== 0;
}

async function refresh() {
  return window.gatewayUI?.refresh();
}

async function changeHospital(hospital, action, body) {
  if (state.busy.has(hospital.id)) return;
  state.busy.add(hospital.id);
  renderHospitals();
  try {
    const result = await api(`hospitals/${encodeURIComponent(hospital.id)}/${action}`, { method: "POST", body: body || {} });
    const updated = result?.hospital || result;
    if (updated?.id) state.hospitals = state.hospitals.map((current) => current.id === updated.id ? updated : current);
    const messages = { test: updated?.connection_status === "offline" ? "ติดต่อโรงพยาบาลไม่ได้ ตรวจสอบปลายทางและทดสอบใหม่" : "ทดสอบการเชื่อมต่อแล้ว", approve: "อนุมัติแล้ว โรงพยาบาลนี้พร้อมให้เลือกบนแอป", suspend: "ระงับการใช้งานแล้ว ผู้ใช้ของหน่วยงานนี้ต้องเข้าสู่ระบบใหม่เมื่อเปิดกลับ", enable: "เปิดใช้งานแล้ว ผู้ใช้สามารถเข้าสู่ระบบได้" };
    notice(messages[action], action === "test" && updated?.connection_status === "offline" ? "error" : "success");
    window.gatewayUI?.invalidate("hospitals");
    window.gatewayUI?.invalidate("updateHospitals");
    await refresh();
  } catch (error) {
    if (action === "suspend") {
      byId("suspend-error").textContent = error.message;
      byId("suspend-error").hidden = false;
    } else notice(error.message, "error");
    throw error;
  } finally {
    state.busy.delete(hospital.id);
    renderHospitals();
  }
}

function setAddOpen(open) {
  byId("add-panel").hidden = !open;
  byId("show-add").setAttribute("aria-expanded", String(open));
  if (open) byId("hospital-code").focus();
}

function resetEditor() {
  state.editing = null;
  byId("hospital-expiry-date").setCustomValidity("");
  byId("hospital-expiry-date").min = "0001-01-01";
  byId("hospital-code").readOnly = false;
  byId("hospital-key").required = true;
  byId("add-heading").textContent = "เพิ่มโรงพยาบาล";
  byId("add-submit").textContent = "บันทึกหน่วยงาน";
  byId("edit-warning").hidden = true;
  byId("form-description").textContent = "หน่วยงานใหม่จะอยู่ในสถานะรออนุมัติ ทดสอบการเชื่อมต่อก่อนเปิดให้เลือกบนแอป";
  byId("key-help").textContent = "ใช้ค่าเดียวกับ HOSPITAL_GATEWAY_KEY ของหน่วยงาน อย่างน้อย 32 ตัวอักษร ระบบจะไม่แสดงค่านี้อีก";
}

function openEditor(hospital) {
  byId("add-form").reset();
  resetEditor();
  state.editing = hospital;
  for (const [field, value] of [["hospital-code", hospital.code], ["hospital-name", hospital.name], ["hospital-province", hospital.province], ["hospital-url", hospital.api_url], ["hospital-start-date", hospital.start_date], ["hospital-expiry-date", hospital.expiry_date]]) byId(field).value = value || "";
  byId("hospital-code").readOnly = true;
  byId("hospital-key").required = false;
  byId("add-heading").textContent = "แก้ไขโรงพยาบาล";
  byId("add-submit").textContent = "บันทึกการแก้ไข";
  byId("edit-warning").hidden = true;
  byId("form-description").textContent = "แก้ไขข้อมูลหน่วยงานและช่วงวันที่ใช้งาน รหัสสถานพยาบาลยังคงเดิม";
  byId("key-help").textContent = "เว้นว่างเพื่อใช้กุญแจเดิม หากเปลี่ยนต้องตรงกับ HOSPITAL_GATEWAY_KEY ของหน่วยงาน และมีอย่างน้อย 32 ตัวอักษร";
  validateDates();
  setAddOpen(true);
  byId("hospital-name").focus();
  byId("add-panel").scrollIntoView({ block: "start" });
}

function validateDates() {
  const start = byId("hospital-start-date").value;
  const expiry = byId("hospital-expiry-date");
  expiry.min = start || "0001-01-01";
  expiry.setCustomValidity(start && expiry.value && expiry.value < start ? "วันหมดอายุต้องเป็นวันเดียวกับหรือหลังวันเริ่มใช้งาน" : "");
}

function changesConfiguration(body, hospital) {
  return Boolean(body.gateway_key) || ["name", "province", "api_url"].some((field) => body[field] !== hospital[field]);
}

function editorBody(form) {
  const body = Object.fromEntries([...new FormData(form)].map(([key, value]) => [key, key === "gateway_key" ? value : value.trim()]));
  body.api_url = body.api_url.replace(/\/+$/, "");
  return body;
}

function confirmEdit(body) {
  const dialog = byId("edit-dialog");
  byId("edit-confirm-hospital").textContent = `${body.name} · ${body.code}`;
  byId("edit-confirm-url").textContent = body.api_url;
  dialog.returnValue = "cancel";
  return new Promise((resolve) => {
    dialog.addEventListener("close", () => resolve(dialog.returnValue === "confirm"), { once: true });
    dialog.showModal();
  });
}

function openSuspend(hospital) {
  state.suspend = hospital;
  byId("suspend-form").reset();
  byId("suspend-error").hidden = true;
  byId("suspend-hospital").textContent = `${hospital.name} · ${hospital.code}`;
  byId("suspend-dialog").showModal();
  byId("suspend-reason").focus();
}

function attachmentMessage(message, type = "info") {
  const box = byId(type === "error" ? "attachments-error" : "attachments-message");
  byId("attachments-error").hidden = true;
  byId("attachments-message").hidden = true;
  box.textContent = message;
  if (type !== "error") box.className = `notice ${type}`;
  box.hidden = !message;
}

function attachmentPath(hospitalID = state.attachmentHospital.id) {
  return `hospitals/${encodeURIComponent(hospitalID)}/attachments`;
}

function attachmentControls() {
  const busy = state.attachmentBusy || state.attachmentLoading;
  for (const id of ["attachment-files", "attachment-category", "attachment-submit", "attachments-refresh"]) byId(id).disabled = busy;
  byId("attachments-close").disabled = state.attachmentBusy;
  byId("attachments-form").setAttribute("aria-busy", String(state.attachmentBusy));
  byId("attachment-list").setAttribute("aria-busy", String(state.attachmentLoading));
}

function renderAttachments() {
  const items = state.attachments.map((attachment) => {
    const item = node("li", "attachment-item");
    const details = node("div", "attachment-details");
    details.append(node("div", "attachment-name", attachment.filename));
    const size = attachment.size < 1024 * 1024 ? `${Math.max(1, Math.ceil(attachment.size / 1024))} KB` : `${(attachment.size / (1024 * 1024)).toFixed(1)} MB`;
    details.append(node("span", "attachment-meta", `${attachmentCategories[attachment.category] || attachment.category} · ${size} · ${when(attachment.created_at)}`));
    const actions = node("div", "attachment-actions");
    const download = node("a", "button secondary compact", "ดาวน์โหลด");
    download.href = `/admin/api/${attachmentPath()}/${encodeURIComponent(attachment.id)}`;
    download.download = attachment.filename;
    download.setAttribute("aria-label", `ดาวน์โหลด ${attachment.filename}`);
    actions.append(download);
    const remove = node("button", "button subtle compact", "ลบ");
    remove.type = "button";
    remove.disabled = state.attachmentBusy || state.attachmentLoading;
    remove.setAttribute("aria-label", `ลบ ${attachment.filename}`);
    remove.addEventListener("click", () => {
      state.attachmentDelete = attachment.id;
      renderAttachments();
      byId("attachment-delete-confirm").focus();
    });
    actions.append(remove);
    item.append(details, actions);
    if (state.attachmentDelete === attachment.id) {
      const confirmation = node("div", "attachment-confirm");
      confirmation.append(node("p", "", `ลบไฟล์ ${attachment.filename} ออกจากโรงพยาบาลนี้หรือไม่?`));
      const cancel = node("button", "button secondary compact", "เก็บไฟล์ไว้");
      cancel.type = "button";
      cancel.disabled = state.attachmentBusy;
      cancel.addEventListener("click", () => { state.attachmentDelete = null; renderAttachments(); byId("attachments-heading").focus(); });
      const confirm = node("button", "button danger compact", state.attachmentBusy ? "กำลังลบ…" : "ยืนยันลบไฟล์");
      confirm.id = "attachment-delete-confirm";
      confirm.type = "button";
      confirm.disabled = state.attachmentBusy;
      confirm.addEventListener("click", () => deleteAttachment(attachment));
      confirmation.append(cancel, confirm);
      item.append(confirmation);
    }
    return item;
  });
  byId("attachment-list").replaceChildren(...items);
  byId("attachments-loading").hidden = !state.attachmentLoading;
  byId("attachments-empty").hidden = !state.attachmentsLoaded || state.attachmentLoading || items.length !== 0;
  attachmentControls();
}

async function loadAttachments(reset = true) {
  const hospitalID = state.attachmentHospital?.id;
  if (!hospitalID || state.attachmentLoading || (!reset && !state.attachmentCursor)) return;
  const request = ++state.attachmentRequest;
  state.attachmentLoading = true;
  const cursor = reset ? "" : state.attachmentCursor;
  if (reset) {
    state.attachments = [];
    state.attachmentCursor = null;
    state.attachmentsLoaded = false;
  }
  renderAttachments();
  const more = byId("attachments-more");
  more.disabled = true;
  try {
    const params = new URLSearchParams({limit:"25"});
    if (cursor) params.set("cursor",cursor);
    const result = await api(attachmentPath(hospitalID) + "?" + params);
    if (request !== state.attachmentRequest || hospitalID !== state.attachmentHospital?.id) return;
    const map = new Map(state.attachments.map(item => [item.id,item]));
    for (const item of result.attachments || []) map.set(item.id,item);
    state.attachments = [...map.values()];
    state.attachmentCursor = result.next_cursor || null;
    state.attachmentTotal = result.total ?? state.attachments.length;
    state.attachmentsLoaded = true;
    more.dataset.retry = "";
    more.textContent = "โหลดเอกสารเพิ่ม";
    more.hidden = !state.attachmentCursor;
  } catch (error) {
    if (request === state.attachmentRequest && hospitalID === state.attachmentHospital?.id) {
      attachmentMessage(error.message,"error");
      more.hidden = false;
      more.dataset.retry = reset ? "reset" : "next";
      more.textContent = "ลองโหลดเอกสารอีกครั้ง";
    }
  } finally {
    if (request === state.attachmentRequest && hospitalID === state.attachmentHospital?.id) {
      state.attachmentLoading = false;
      more.disabled = false;
      renderAttachments();
    }
  }
}

function openAttachments(hospital) {
  if (state.attachmentBusy) return;
  state.attachmentRequest++;
  state.attachmentHospital = hospital;
  state.attachments = [];
  state.attachmentsLoaded = false;
  state.attachmentCursor = null;
  byId("attachments-more").hidden = true;
  state.attachmentLoading = false;
  state.attachmentDelete = null;
  byId("attachments-form").reset();
  attachmentMessage("");
  byId("attachments-hospital").textContent = `${hospital.name} · ${hospital.code}`;
  byId("attachments-panel").hidden = false;
  byId("attachments-heading").focus();
  byId("attachments-panel").scrollIntoView({ block: "start" });
  loadAttachments();
}

function closeAttachments(restoreFocus = true) {
  const hospitalID = state.attachmentHospital?.id;
  state.attachmentRequest++;
  state.attachmentHospital = null;
  state.attachments = [];
  state.attachmentLoading = false;
  state.attachmentDelete = null;
  byId("attachments-panel").hidden = true;
  byId("attachments-form").reset();
  byId("attachment-list").replaceChildren();
  attachmentMessage("");
  if (restoreFocus) {
    const trigger = [...document.querySelectorAll('[data-action="attachments"]')].find((button) => button.dataset.hospital === hospitalID);
    (trigger || byId("show-add")).focus();
  }
}

async function refreshAttachmentAudit() {
  window.gatewayUI?.invalidate("audit");
}

async function deleteAttachment(attachment) {
  if (state.attachmentBusy || !state.attachmentHospital) return;
  const hospitalID = state.attachmentHospital.id;
  state.attachmentBusy = true;
  attachmentMessage("");
  renderAttachments();
  renderHospitals();
  try {
    await api(`${attachmentPath(hospitalID)}/${encodeURIComponent(attachment.id)}`, { method: "DELETE" });
    if (hospitalID !== state.attachmentHospital?.id) return;
    state.attachments = state.attachments.filter((current) => current.id !== attachment.id);
    state.attachmentDelete = null;
    attachmentMessage(`ลบไฟล์ ${attachment.filename} แล้ว`, "success");
    await refreshAttachmentAudit();
  } catch (error) {
    if (hospitalID === state.attachmentHospital?.id) attachmentMessage(error.message, "error");
  } finally {
    state.attachmentBusy = false;
    if (state.attachmentHospital) renderAttachments();
    renderHospitals();
    if (hospitalID === state.attachmentHospital?.id) byId("attachments-heading").focus();
  }
}

function retainFiles(files) {
  const transfer = new DataTransfer();
  for (const file of files) transfer.items.add(file);
  byId("attachment-files").files = transfer.files;
}

byId("attachments-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (state.attachmentBusy || state.attachmentLoading || !state.attachmentHospital) return;
  const files = [...byId("attachment-files").files];
  if (!files.length) return;
  const invalid = files.find((file) => file.size === 0 || file.size > 10 * 1024 * 1024 || !/\.(pdf|jpe?g|png|gif|webp)$/i.test(file.name));
  if (invalid) {
    attachmentMessage(`ไฟล์ ${invalid.name} ต้องเป็น PDF, JPG, PNG, GIF หรือ WebP ที่ไม่ว่างและมีขนาดไม่เกิน 10 MB`, "error");
    return;
  }
  const hospitalID = state.attachmentHospital.id;
  const category = byId("attachment-category").value;
  let uploaded = 0;
  state.attachmentBusy = true;
  state.attachmentDelete = null;
  renderAttachments();
  renderHospitals();
  try {
    for (const file of files) {
      if (hospitalID !== state.attachmentHospital?.id) break;
      attachmentMessage(`กำลังอัปโหลด ${uploaded + 1} จาก ${files.length}: ${file.name}`);
      const body = new FormData();
      body.append("category", category);
      body.append("file", file);
      const attachment = await api(attachmentPath(hospitalID), { method: "POST", body });
      uploaded++;
      if (hospitalID !== state.attachmentHospital?.id) break;
      state.attachments.unshift(attachment);
      state.attachmentsLoaded = true;
      retainFiles(files.slice(uploaded));
      renderAttachments();
    }
    if (hospitalID === state.attachmentHospital?.id) attachmentMessage(`แนบไฟล์สำเร็จ ${uploaded} ไฟล์`, "success");
  } catch (error) {
    if (hospitalID === state.attachmentHospital?.id) attachmentMessage(`อัปโหลดสำเร็จ ${uploaded} จาก ${files.length} ไฟล์ ${error.message} ไฟล์ที่ยังไม่สำเร็จยังคงอยู่ในช่องเลือกไฟล์ ตรวจรายการก่อนลองอีกครั้ง`, "error");
  } finally {
    state.attachmentBusy = false;
    if (state.attachmentHospital) renderAttachments();
    renderHospitals();
    if (uploaded) await refreshAttachmentAudit();
  }
});
byId("attachments-close").addEventListener("click", () => { if (!state.attachmentBusy) closeAttachments(); });
byId("attachments-refresh").addEventListener("click", () => { attachmentMessage(""); loadAttachments(); });

byId("login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = byId("login-submit");
  button.disabled = true;
  button.textContent = "กำลังเข้าสู่ระบบ…";
  notice("");
  try {
    const result = await api("login", { method: "POST", body: { username: byId("username").value.trim(), password: byId("password").value } });
    state.csrf = result.csrf_token;
    byId("password").value = "";
    setAuthenticated(true);
    await refresh();
  } catch (error) { notice(error.message, "error"); }
  finally { button.disabled = false; button.textContent = "เข้าสู่ระบบ"; }
});

byId("logout").addEventListener("click", async () => {
  const button = byId("logout");
  button.disabled = true;
  try { await api("logout", { method: "POST", body: {} }); setAuthenticated(false); notice(""); }
  catch (error) { notice(error.message, "error"); }
  finally { button.disabled = false; }
});

byId("add-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = byId("add-submit");
  if (button.disabled) return;
  validateDates();
  if (!event.currentTarget.reportValidity()) return;
  button.disabled = true;
  try {
    const body = editorBody(event.currentTarget);
    const editing = state.editing;
    const resetsApproval = editing && changesConfiguration(body, editing);
    if (resetsApproval && !await confirmEdit(body)) return;
    await api(editing ? `hospitals/${encodeURIComponent(editing.id)}` : "hospitals", { method: editing ? "PUT" : "POST", body });
    window.gatewayUI?.invalidate("hospitals");
    window.gatewayUI?.invalidate("updateHospitals");
    byId("add-form").reset();
    resetEditor();
    setAddOpen(false);
    notice(editing ? resetsApproval ? "บันทึกการแก้ไขและยกเลิก session แล้ว กรุณาทดสอบและอนุมัติใหม่" : "บันทึกวันที่ใช้งานแล้ว" : "เพิ่มโรงพยาบาลแล้ว แนบไฟล์ได้ที่เอกสารแนบ และทดสอบการเชื่อมต่อก่อนอนุมัติ", "success");
    await refresh();
  } catch (error) { notice(error.message, "error"); }
  finally { button.disabled = false; }
});
byId("add-form").addEventListener("input", (event) => {
  validateDates();
  byId("edit-warning").hidden = !state.editing || !changesConfiguration(editorBody(event.currentTarget), state.editing);
});

byId("suspend-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const reason = byId("suspend-reason").value.trim();
  if (!reason) { byId("suspend-reason").setCustomValidity("กรุณาระบุเหตุผล"); byId("suspend-reason").reportValidity(); return; }
  byId("suspend-submit").disabled = true;
  byId("suspend-cancel").disabled = true;
  try { await changeHospital(state.suspend, "suspend", { reason }); byId("suspend-dialog").close(); }
  catch (_) { /* The dialog retains the reason and displays its error. */ }
  finally { byId("suspend-submit").disabled = false; byId("suspend-cancel").disabled = false; }
});
byId("suspend-reason").addEventListener("input", () => byId("suspend-reason").setCustomValidity(""));
byId("suspend-cancel").addEventListener("click", () => byId("suspend-dialog").close());
byId("suspend-dialog").addEventListener("cancel", (event) => { if (byId("suspend-submit").disabled) event.preventDefault(); });
byId("show-add").addEventListener("click", () => { byId("add-form").reset(); resetEditor(); setAddOpen(true); });
for (const id of ["cancel-add", "reset-add"]) byId(id).addEventListener("click", () => { byId("add-form").reset(); resetEditor(); setAddOpen(false); byId("show-add").focus(); });
byId("search").addEventListener("input", () => window.gatewayUI?.hospitalsChanged());
byId("filter").addEventListener("change", () => window.gatewayUI?.hospitalsChanged(true));
byId("refresh").addEventListener("click", () => refresh().catch((error) => notice(error.message, "error")));

async function start() {
  try {
    const result = await api("session");
    state.csrf = result.csrf_token;
    setAuthenticated(true);
    await refresh();
  } catch (error) {
    setAuthenticated(false);
    if (error.status !== 401) notice(error.message, "error");
  }
}
window.addEventListener("gateway-ui-ready",start,{once:true});
