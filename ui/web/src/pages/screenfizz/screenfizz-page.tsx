import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Search, X, ExternalLink, RefreshCw, Send } from "lucide-react";
import { PageHeader } from "@/components/shared/page-header";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useHttp } from "@/hooks/use-ws";

type Row = Record<string, any>;
type Dashboard = { stats: Record<string, number>; businesses: Row[]; prospects: Row[] };

function businessOf(row: Row): Row { return row.screenfizz_businesses ?? {}; }
function text(value: unknown) { return typeof value === "string" ? value : ""; }
function prettyDate(value: unknown) { const date = text(value); return date ? new Date(date).toLocaleDateString() : "—"; }

export function ScreenFizzPage() {
  const http = useHttp();
  const [data, setData] = useState<Dashboard>({ stats: {}, businesses: [], prospects: [] });
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState("");
  const [town, setTown] = useState("");
  const [emailOnly, setEmailOnly] = useState(false);
  const [websiteOnly, setWebsiteOnly] = useState(false);
  const [selected, setSelected] = useState<Row | null>(null);
  const [review, setReview] = useState<Row | null>(null);
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");

  const load = useCallback(async () => {
    setLoading(true); setError("");
    try { setData(await http.get<Dashboard>("/v1/screenfizz/dashboard")); }
    catch (e) { setError(e instanceof Error ? e.message : "Unable to load ScreenFizz"); }
    finally { setLoading(false); }
  }, [http]);
  useEffect(() => { void load(); }, [load]);

  const matches = useCallback((row: Row) => {
    const b = businessOf(row); const q = query.trim().toLowerCase();
    if (q && ![b.business_name, b.email, b.town, b.category].some((v) => text(v).toLowerCase().includes(q))) return false;
    if (category && b.category !== category) return false;
    if (town && b.town !== town) return false;
    if (emailOnly && !text(b.email)) return false;
    return !websiteOnly || !!text(b.website);
  }, [query, category, town, emailOnly, websiteOnly]);
  const businesses = useMemo(() => data.businesses.filter(matches), [data.businesses, matches]);
  const prospects = useMemo(() => data.prospects.filter(matches), [data.prospects, matches]);
  const pending = prospects.filter((p) => p.status === "ready_to_send" || p.status === "pending_review");
  const approved = prospects.filter((p) => p.status === "approved");
  const categories = useMemo(() => [...new Set(data.businesses.map((b) => text(b.category)).filter(Boolean))].sort(), [data.businesses]);
  const towns = useMemo(() => [...new Set(data.businesses.map((b) => text(b.town)).filter(Boolean))].sort(), [data.businesses]);

  const mutate = async (id: string, values: Row) => { await http.patch(`/v1/screenfizz/prospects/${id}`, values); await load(); };
  const chooseReview = (prospect: Row | null) => { setReview(prospect); setSubject(text(prospect?.email_subject)); setBody(text(prospect?.email_body)); };
  const current = review ?? pending[0] ?? null;
  const stat = (label: string, key: string) => <Card key={key} className="gap-2 py-4"><CardContent className="px-5"><p className="text-sm text-muted-foreground">{label}</p><p className="mt-1 text-2xl font-semibold">{data.stats[key] ?? 0}</p></CardContent></Card>;

  return <div className="space-y-6 p-4 sm:p-6">
    <PageHeader title="ScreenFizz" description="Internal lead review and approval workspace." actions={<Button variant="outline" onClick={() => void load()} disabled={loading}><RefreshCw /> Refresh</Button>} />
    <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
      <div className="relative max-w-md flex-1"><Search className="absolute left-3 top-2.5 size-4 text-muted-foreground" /><Input className="pl-9 text-base md:text-sm" placeholder="Search business, email, town or category" value={query} onChange={(e) => setQuery(e.target.value)} /></div>
      <select className="h-9 rounded-md border bg-background px-3 text-base md:text-sm" value={category} onChange={(e) => setCategory(e.target.value)}><option value="">All categories</option>{categories.map((v) => <option key={v}>{v}</option>)}</select>
      <select className="h-9 rounded-md border bg-background px-3 text-base md:text-sm" value={town} onChange={(e) => setTown(e.target.value)}><option value="">All towns</option>{towns.map((v) => <option key={v}>{v}</option>)}</select>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={emailOnly} onChange={(e) => setEmailOnly(e.target.checked)} />Has email</label>
      <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={websiteOnly} onChange={(e) => setWebsiteOnly(e.target.checked)} />Has website</label>
    </div>
    {error && <Card className="border-destructive"><CardContent className="p-4 text-destructive">{error}</CardContent></Card>}
    <Tabs defaultValue="review">
      <TabsList className="h-auto flex-wrap justify-start"><TabsTrigger value="overview">Overview</TabsTrigger><TabsTrigger value="businesses">Businesses</TabsTrigger><TabsTrigger value="prospects">Prospects</TabsTrigger><TabsTrigger value="review">Email Review</TabsTrigger><TabsTrigger value="approved">Approved</TabsTrigger><TabsTrigger value="analytics">Analytics</TabsTrigger></TabsList>
      <TabsContent value="overview" className="space-y-4"><div className="grid grid-cols-2 gap-3 lg:grid-cols-6">{stat("Businesses Found","businesses_found")}{stat("Prospects","prospects")}{stat("Ready to Send","pending_review")}{stat("Approved","approved")}{stat("Sent","sent")}{stat("Replies","replies")}</div></TabsContent>
      <TabsContent value="businesses"><Card><CardHeader><CardTitle>Businesses</CardTitle></CardHeader><CardContent><DataTable headers={["Business Name","Category","Town","Website","Email","Phone","Rating","Found Date","Action"]} rows={businesses.map((b) => [text(b.business_name),text(b.category),text(b.town),<Website value={text(b.website)} />,text(b.email),text(b.phone),String(b.rating ?? "—"),prettyDate(b.created_at),<Button size="sm" variant="outline" onClick={() => setSelected(b)}>View</Button>])} /></CardContent></Card></TabsContent>
      <TabsContent value="prospects"><Card><CardHeader><CardTitle>Prospects</CardTitle></CardHeader><CardContent><DataTable headers={["Business","AI Summary","Recommended Use Case","Opportunity Score","Status"]} rows={prospects.map((p) => { const b=businessOf(p); return [<button className="text-left font-medium hover:underline" onClick={() => setSelected(p)}>{text(b.business_name)}</button>,text(p.business_summary)||"—",text(p.recommended_use_case)||"—",String(p.likely_needs_digital_signage ?? "—"),text(p.status)||"—"]; })} /></CardContent></Card></TabsContent>
      <TabsContent value="review"><ReviewCard prospect={current} subject={subject} body={body} setSubject={setSubject} setBody={setBody} onSave={() => current && void mutate(current.id,{email_subject:subject,email_body:body,status:"ready_to_send"})} onApprove={() => current && void mutate(current.id,{status:"approved"})} onReject={() => current && void mutate(current.id,{status:"skipped"})} onRegenerate={() => current && void mutate(current.id,{regenerate:true})} onNext={() => chooseReview(pending.find((p) => p.id !== current?.id) ?? null)} /></TabsContent>
      <TabsContent value="approved"><Card><CardHeader><CardTitle>Approved emails</CardTitle></CardHeader><CardContent><DataTable headers={["Business","Email","Approved At","Actions"]} rows={approved.map((p) => {const b=businessOf(p); return [text(b.business_name),text(b.email),prettyDate(p.updated_at ?? p.created_at),<div className="flex gap-2"><Button size="sm" variant="outline" disabled title="Sending stays in the ScreenFizz pipeline"><Send/> Send Now</Button><Button size="sm" variant="outline" onClick={() => void mutate(p.id,{status:"ready_to_send"})}>Move Back To Review</Button></div>];})} /></CardContent></Card></TabsContent>
      <TabsContent value="analytics"><div className="grid grid-cols-2 gap-3 lg:grid-cols-4">{stat("Businesses Imported","businesses_found")}{stat("Emails Generated","emails_generated")}{stat("Approved","approved")}{stat("Sent","sent")}{stat("Opened","opened")}{stat("Clicked","clicked")}{stat("Replies","replies")}</div></TabsContent>
    </Tabs>
    {selected && <aside className="fixed inset-y-0 right-0 z-40 w-full max-w-lg overflow-y-auto border-l bg-background p-6 shadow-2xl safe-right"><button className="absolute right-4 top-4" onClick={() => setSelected(null)}><X /></button><h2 className="text-xl font-semibold">{text(businessOf(selected).business_name) || "Business details"}</h2><div className="mt-6 space-y-5 text-sm"><Detail label="Website"><Website value={text(businessOf(selected).website)} /></Detail><Detail label="Email">{text(businessOf(selected).email)||"—"}</Detail><Detail label="Town">{text(businessOf(selected).town)||"—"}</Detail><Detail label="AI Summary">{text(selected.business_summary)||"—"}</Detail><Detail label="Recommended Use Case">{text(selected.recommended_use_case)||"—"}</Detail><Detail label="Personalisation Line">{text(selected.personalisation_line)||"—"}</Detail></div></aside>}
  </div>;
}

function DataTable({headers,rows}:{headers:string[];rows:any[][]}) { return <div className="overflow-x-auto"><table className="min-w-[900px] w-full text-sm"><thead><tr className="border-b text-left text-muted-foreground">{headers.map((h)=><th key={h} className="px-3 py-3 font-medium">{h}</th>)}</tr></thead><tbody>{rows.map((row,i)=><tr key={i} className="border-b last:border-0">{row.map((cell,j)=><td key={j} className="px-3 py-3 align-top">{cell||"—"}</td>)}</tr>)}{rows.length===0&&<tr><td className="px-3 py-8 text-muted-foreground" colSpan={headers.length}>No matching records.</td></tr>}</tbody></table></div>; }
function Website({value}:{value:string}) { return value ? <a className="inline-flex items-center gap-1 text-primary hover:underline" target="_blank" rel="noreferrer" href={value.startsWith("http")?value:`https://${value}`}>{value}<ExternalLink className="size-3"/></a> : <>—</>; }
function Detail({label,children}:{label:string;children:ReactNode}) { return <div><p className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</p><div className="whitespace-pre-wrap leading-6">{children}</div></div>; }
function ReviewCard(props:any) { const {prospect,subject,body,setSubject,setBody,onSave,onApprove,onReject,onRegenerate,onNext}=props; if(!prospect)return <Card><CardContent className="p-8 text-center text-muted-foreground">No pending emails to review.</CardContent></Card>; const b=businessOf(prospect); return <Card><CardHeader><CardTitle>{text(b.business_name)}</CardTitle><p className="text-sm text-muted-foreground"><Website value={text(b.website)} /> · {text(b.town)||"Town unavailable"}</p></CardHeader><CardContent className="space-y-5"><div className="grid gap-4 md:grid-cols-3"><Detail label="AI Summary">{text(prospect.business_summary)||"—"}</Detail><Detail label="Recommended Use Case">{text(prospect.recommended_use_case)||"—"}</Detail><Detail label="Personalisation Line">{text(prospect.personalisation_line)||"—"}</Detail></div><div><label className="text-sm font-medium">Subject</label><Input className="mt-2 text-base md:text-sm" value={subject} onChange={(e)=>setSubject(e.target.value)}/></div><div><label className="text-sm font-medium">Email Body</label><Textarea className="mt-2 min-h-64 text-base md:text-sm" value={body} onChange={(e)=>setBody(e.target.value)}/></div><div className="flex flex-wrap gap-2 border-t pt-4"><Button onClick={onApprove}>Approve</Button><Button variant="destructive" onClick={onReject}>Reject</Button><Button variant="outline" onClick={onRegenerate}>Regenerate</Button><Button variant="secondary" onClick={onSave}>Save Changes</Button><Button variant="ghost" onClick={onNext}>Next Prospect</Button></div></CardContent></Card>; }
