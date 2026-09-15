import assert from "node:assert/strict";
import test from "node:test";
import { claimHint, hintTexts, noteReports, planRows, proposalKey, proposalLabel } from "./plan-surface.js";

test("plan rows use only leading markers and preserve current order", () => {
  const value=planRows("# P\n## Current work order\nDo this\n## Items\n[x] 2t done\n[~] 2u live\n[ ] 3 next\n[!] 4 blocked\n[-] 5 dropped");
  assert.equal(value.order,"Do this"); assert.deepEqual(value.items.map((item)=>item.marker),["x","~"," ","!","-"]);
});
test("proposal persistence distinguishes reused ids with different edits",()=>{
  const first={id:"p",kind:"add",path:"plan.md",old_text:"a",new_text:"ab",item_id:"2t"};
  assert.notEqual(proposalKey(first),proposalKey({...first,new_text:"ac"}));
});
test("notes reports are heading-delimited and proposal labels are inert text",()=>{
  assert.equal(noteReports("## 2026 — v1 (2t)\nbody\n## next\nother")[0].body,"body");
  assert.equal(proposalLabel({kind:"add",path:"plan.md",item_id:"2t"}),"add · plan.md · 2t");
});
test("the four shipped hint texts persist as shown across a restart",()=>{
  assert.equal(Object.keys(hintTexts).length,4);
  const values=new Map(); const storage={getItem:(key)=>values.get(key)||null,setItem:(key,value)=>values.set(key,value)};
  for(const id of Object.keys(hintTexts)){assert.equal(claimHint(storage,id),hintTexts[id]);assert.equal(claimHint(storage,id),"");}
  for(const id of Object.keys(hintTexts)) assert.equal(claimHint(storage,id),"");
});
