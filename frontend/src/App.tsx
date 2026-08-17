import { useEffect, useMemo, useState } from "react";
import {
  Activity,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  CircleStop,
  Copy,
  HeartHandshake,
  KeyRound,
  LayoutDashboard,
  LoaderCircle,
  LockKeyhole,
  Play,
  Plus,
  Power,
  PowerOff,
  RefreshCw,
  ShieldCheck,
  Trash2,
  WalletCards,
} from "lucide-react";

type Task = {
  id: string;
  name: string;
  openSeaUrl: string;
  walletAddress: string;
  network: { name: string; chainId: number; rpcUrl: string };
  contract: string;
  saleGate: { function: string; expect: boolean; pollIntervalSeconds?: number; pollIntervalMilliseconds?: number };
  mint: { method: string; quantity: number; rawCalldata: string; valueWei: string; maxGasPriceWei: string; maxTotalCostWei: string };
  openSea?: { slug: string; collection: string; stage?: OpenSeaStage; stages?: OpenSeaStage[]; targetQuantity?: number; spentWei?: string };
  enabled: boolean;
  status: string;
  lastCheckedAt?: string;
  lastTxHash?: string;
  lastError?: string;
};

type OpenSeaStage = {
  label: string;
  stageType: string;
  stageIndex: number;
  startTime: string;
  endTime: string;
  maxTotalMintableByWallet: number;
  price: string;
  priceWei: string;
  priceSymbol: string;
  priceContract: string;
  autoMintSupported: boolean;
  blockReason?: string;
};

type OpenSeaDrop = {
  url: string;
  slug: string;
  name: string;
  chain: string;
  contract: string;
  verified: boolean;
  approved: boolean;
  externalUrl: string;
  twitterUsername: string;
  maxSupply: number;
  totalSupply: number;
  stages: OpenSeaStage[];
  riskFlags: string[];
};

type OpenSeaTaskInput = {
  name: string;
  openSeaUrl: string;
  walletAddresses: string[];
  quantityPerWallet: number;
  rpcUrl: string;
  pollIntervalMilliseconds: number;
  maxGasPriceGwei: string;
  maxTotalCostEth: string;
};

type Dashboard = { dataDir: string; wallets: string[]; tasks: Task[]; running: boolean };
type GeneratedWalletBatch = { mnemonic: string; addresses: string[] };
type Page = "dashboard" | "wallets" | "tasks" | "donation";

declare global {
  interface Window {
    go?: {
      main?: {
        App: {
          Dashboard: () => Promise<Dashboard>;
          ImportPrivateKey: (privateKey: string, password: string) => Promise<string>;
          ImportPrivateKeys: (privateKeys: string[], password: string) => Promise<string[]>;
          ImportMnemonic: (mnemonic: string, password: string, count: number) => Promise<string[]>;
          GenerateMnemonicWallets: (password: string, count: number) => Promise<GeneratedWalletBatch>;
          InspectOpenSeaDrop: (url: string) => Promise<OpenSeaDrop>;
          CreateOpenSeaTasks: (input: OpenSeaTaskInput) => Promise<Task[]>;
          SetTaskEnabled: (id: string, enabled: boolean) => Promise<void>;
          SetCollectionTasksEnabled: (chainId: number, contract: string, enabled: boolean) => Promise<void>;
          DeleteCollectionTasks: (chainId: number, contract: string) => Promise<number>;
          ArmAndRun: (password: string, confirmed: boolean) => Promise<void>;
          StopRunner: () => Promise<void>;
        };
      };
    };
  }
}

const shortAddress = (address: string) => `${address.slice(0, 7)}...${address.slice(-5)}`;
const bridge = () => window.go?.main?.App;
const DONATION_ADDRESS = "0xd439325794932c3ccd45affa85effe5363af1ca8";

export default function App() {
  const [page, setPage] = useState<Page>("dashboard");
  const [data, setData] = useState<Dashboard>({ dataDir: "", wallets: [], tasks: [], running: false });
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(true);

  const refresh = async () => {
    const api = bridge();
    if (!api) {
      setNotice("Desktop bridge unavailable. Start through Wails.");
      setLoading(false);
      return;
    }
    try {
      setData(await api.Dashboard());
    } catch (error) {
      setNotice(String(error));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void refresh(); }, []);
  useEffect(() => {
    const timer = window.setInterval(() => { void refresh(); }, 2500);
    return () => window.clearInterval(timer);
  }, []);

  const activeTasks = useMemo(() => (data.tasks ?? []).filter((task) => task.enabled).length, [data.tasks]);
  const showNotice = (message: string) => { setNotice(message); window.setTimeout(() => setNotice(""), 6000); };

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand"><div className="brand-mark"><img src="/mintdesk-robinhood-logo.svg" alt="" /></div><span>Mint Desk</span></div>
        <nav>
          <NavButton active={page === "dashboard"} icon={<LayoutDashboard size={18} />} label="Overview" onClick={() => setPage("dashboard")} />
          <NavButton active={page === "wallets"} icon={<WalletCards size={18} />} label="Wallets" onClick={() => setPage("wallets")} />
          <NavButton active={page === "tasks"} icon={<CheckCircle2 size={18} />} label="Mint tasks" onClick={() => setPage("tasks")} />
          <NavButton active={page === "donation"} icon={<HeartHandshake size={18} />} label="Donate" onClick={() => setPage("donation")} />
        </nav>
        <div className="sidebar-foot"><ShieldCheck size={17} /><span>Local encrypted store</span></div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div>
            <p className="eyebrow">LOCAL MINT OPERATIONS</p>
            <h1>{page === "dashboard" ? "Command center" : page === "wallets" ? "Wallet vault" : page === "tasks" ? "Mint tasks" : "Support Mint Desk"}</h1>
          </div>
          <div className="top-actions">
            <span className={`runner-state ${data.running ? "live" : "idle"}`}><i />{data.running ? "Runner armed" : "Runner idle"}</span>
            <button className="icon-button" title="Refresh" onClick={() => void refresh()}><RefreshCw size={17} /></button>
          </div>
        </header>

        {notice && <div className="notice">{notice}</div>}
        {loading ? <div className="loading"><LoaderCircle className="spin" size={22} /> Loading local vault</div> : null}
        {!loading && page === "dashboard" && <Overview data={data} activeTasks={activeTasks} onNavigate={setPage} onNotice={showNotice} refresh={refresh} />}
        {!loading && page === "wallets" && <WalletPage data={data} refresh={refresh} onNotice={showNotice} />}
        {!loading && page === "tasks" && <TaskPage data={data} refresh={refresh} onNotice={showNotice} />}
        {!loading && page === "donation" && <DonationPage onNotice={showNotice} />}
      </main>
    </div>
  );
}

function DonationPage({ onNotice }: { onNotice: (message: string) => void }) {
  const copyAddress = async () => {
    try {
      await navigator.clipboard.writeText(DONATION_ADDRESS);
      onNotice("Donation address copied.");
    } catch (error) {
      onNotice(`Copy failed: ${String(error)}`);
    }
  };

  return <div className="view donation-view">
    <section className="donation-card">
      <div className="donation-copy">
        <div className="donation-icon"><HeartHandshake size={25} /></div>
        <p className="eyebrow">COMMUNITY SUPPORT</p>
        <h2>Support continued Mint Desk development</h2>
        <p className="donation-intro">If this open-source tool is useful to you, you can support continued security, compatibility, and usability work with an EVM-wallet donation.</p>
        <div className="network-badge"><ShieldCheck size={15} />EVM wallets and EVM networks only</div>
        <div className="donation-address">
          <span>Donation address</span>
          <code>{DONATION_ADDRESS}</code>
        </div>
        <button className="primary-button donation-copy-button" onClick={() => void copyAddress()}><Copy size={17} />Copy address</button>
        <p className="donation-warning">Before sending, verify that the full address shown by your wallet matches this page and confirm that the selected EVM network and token are supported. Blockchain transfers are generally irreversible.</p>
      </div>
      <div className="qr-panel">
        <div className="qr-frame"><img src="/evm-donation-qr.png" alt={`EVM donation address ${DONATION_ADDRESS}`} /></div>
        <strong>Scan to fill the address</strong>
        <span>The QR code contains only the EVM address above, with no amount or network parameters.</span>
      </div>
    </section>
  </div>;
}

function NavButton({ active, icon, label, onClick }: { active: boolean; icon: React.ReactNode; label: string; onClick: () => void }) {
  return <button className={`nav-button ${active ? "active" : ""}`} onClick={onClick}>{icon}<span>{label}</span></button>;
}

function Overview({ data, activeTasks, onNavigate, onNotice, refresh }: { data: Dashboard; activeTasks: number; onNavigate: (page: Page) => void; onNotice: (message: string) => void; refresh: () => Promise<void> }) {
  const [password, setPassword] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const enabledTasks = useMemo(() => data.tasks.filter((task) => task.enabled), [data.tasks]);
  const run = async () => {
    try {
      await bridge()!.ArmAndRun(password, confirmed);
      setPassword("");
      onNotice("Runner started. Scheduled tasks will begin watching one minute before mint.");
      await refresh();
    } catch (error) { onNotice(String(error)); }
  };
  const stop = async () => { await bridge()!.StopRunner(); onNotice("Runner stopped."); await refresh(); };

  return <div className="view overview">
    <section className="metric-grid">
      <Metric icon={<WalletCards />} label="Imported wallets" value={String(data.wallets.length)} action="Manage" onClick={() => onNavigate("wallets")} />
      <Metric icon={<CheckCircle2 />} label="Enabled tasks" value={String(activeTasks)} action="Configure" onClick={() => onNavigate("tasks")} />
      <Metric icon={<Activity />} label="Runner status" value={data.running ? "Armed" : "Idle"} tone={data.running ? "green" : "muted"} />
    </section>
    <section className="runner-panel">
      <div className="runner-heading"><div><p className="eyebrow">EXECUTION CONTROL</p><h2>{data.running ? "Scheduled tasks armed" : "Arm the task runner"}</h2></div><LockKeyhole size={23} /></div>
      {data.running ? <button className="danger-button" onClick={() => void stop()}><CircleStop size={17} />Stop runner</button> : <div className="arm-row">
        <label className="field compact"><span>Keystore password</span><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Required to sign locally" /></label>
        <label className="check-row"><input type="checkbox" checked={confirmed} onChange={(event) => setConfirmed(event.target.checked)} /><span>I understand this may broadcast real transactions.</span></label>
        <button className="primary-button" onClick={() => void run()}><Play size={17} />Arm & start</button>
      </div>}
    </section>
    <section className="task-panel">
      <div className="section-title"><div><p className="eyebrow">ACTIVE QUEUE</p><h2>Task activity</h2></div><button className="text-button" onClick={() => onNavigate("tasks")}>View all</button></div>
      {enabledTasks.length === 0 ? <EmptyState title="No enabled mint tasks" action="Manage collections" onClick={() => onNavigate("tasks")} /> : <div className="task-list">{enabledTasks.slice(0, 5).map((task) => <TaskRow key={task.id} task={task} />)}</div>}
    </section>
  </div>;
}

function WalletPage({ data, refresh, onNotice }: { data: Dashboard; refresh: () => Promise<void>; onNotice: (message: string) => void }) {
  const [mode, setMode] = useState<"keys" | "mnemonic" | "generate">("keys");
  const [secret, setSecret] = useState("");
  const [password, setPassword] = useState("");
  const [count, setCount] = useState(3);
  const [generated, setGenerated] = useState<GeneratedWalletBatch | null>(null);

  const changeMode = (next: "keys" | "mnemonic" | "generate") => {
    setMode(next);
    setSecret("");
    setGenerated(null);
  };

  const importWallet = async () => {
    try {
      let addresses: string[];
      if (mode === "keys") {
        const keys = secret.split(/[\s,;]+/).map((key) => key.trim()).filter(Boolean);
        addresses = await bridge()!.ImportPrivateKeys(keys, password);
      } else if (mode === "mnemonic") {
        addresses = await bridge()!.ImportMnemonic(secret, password, count);
      } else {
        const batch = await bridge()!.GenerateMnemonicWallets(password, count);
        setGenerated(batch);
        addresses = batch.addresses;
      }
      setSecret("");
      setPassword("");
      onNotice(`Added ${addresses.length} encrypted wallet${addresses.length === 1 ? "" : "s"}.`);
      await refresh();
    } catch (error) { onNotice(String(error)); }
  };

  const canSubmit = password.length > 0 && (mode === "generate" || secret.trim().length > 0);

  return <div className="view two-column">
    <section className="form-panel">
      <div className="section-title"><div><p className="eyebrow">ADD WALLETS</p><h2>Local wallet vault</h2></div><KeyRound size={22} /></div>
      <div className="segmented wallet-modes">
        <button className={mode === "keys" ? "selected" : ""} onClick={() => changeMode("keys")}>Private keys</button>
        <button className={mode === "mnemonic" ? "selected" : ""} onClick={() => changeMode("mnemonic")}>Import phrase</button>
        <button className={mode === "generate" ? "selected" : ""} onClick={() => changeMode("generate")}>Generate</button>
      </div>
      {mode === "keys" && <label className="field"><span>Private keys</span><textarea rows={6} value={secret} onChange={(event) => setSecret(event.target.value)} spellCheck={false} placeholder={"Paste one private key per line\n0x...\n0x..."} /></label>}
      {mode === "mnemonic" && <label className="field"><span>Recovery phrase</span><textarea rows={5} value={secret} onChange={(event) => setSecret(event.target.value)} spellCheck={false} placeholder="word word word ..." /></label>}
      {(mode === "mnemonic" || mode === "generate") && <label className="field"><span>Addresses to derive</span><input type="number" min="1" max="20" value={count} onChange={(event) => setCount(Number(event.target.value))} /></label>}
      {mode === "generate" && <p className="generation-note">A new 24-word recovery phrase will derive addresses using <code>m/44'/60'/0'/0/i</code>. The phrase is shown once and is never saved by Mint Desk.</p>}
      <label className="field"><span>Keystore password</span><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Encrypts every wallet in this batch" /></label>
      <button className="primary-button full" disabled={!canSubmit} onClick={() => void importWallet()}><LockKeyhole size={17} />{mode === "generate" ? `Generate ${count} encrypted wallet${count === 1 ? "" : "s"}` : "Import encrypted wallets"}</button>
      {generated && <div className="recovery-result">
        <div className="field-label"><span>Save this recovery phrase now</span><button className="text-button" onClick={() => { void navigator.clipboard.writeText(generated.mnemonic); onNotice("Recovery phrase copied."); }}><Copy size={14} /> Copy</button></div>
        <code>{generated.mnemonic}</code>
        <p>Mint Desk will not show this phrase again. Store it offline before clearing it from the screen.</p>
        <button className="secondary-button" onClick={() => setGenerated(null)}>Clear from screen</button>
      </div>}
    </section>
    <section className="wallet-list-panel">
      <div className="section-title"><div><p className="eyebrow">LOCAL ADDRESSES</p><h2>{data.wallets.length} imported</h2></div></div>
      {data.wallets.length === 0 ? <EmptyState title="Your wallet vault is empty" /> : <div className="wallet-list">{data.wallets.map((wallet) => <div className="wallet-row" key={wallet}><div className="wallet-icon"><WalletCards size={18} /></div><code>{wallet}</code><button className="icon-button" title="Copy address" onClick={() => { navigator.clipboard.writeText(wallet); onNotice("Address copied."); }}><Copy size={15} /></button></div>)}</div>}
      <p className="subtle">Keys are encrypted locally. The application does not keep the imported plaintext secret in task data.</p>
    </section>
  </div>;
}

function TaskPage({ data, refresh, onNotice }: { data: Dashboard; refresh: () => Promise<void>; onNotice: (message: string) => void }) {
  const [openSeaUrl, setOpenSeaUrl] = useState("");
  const [drop, setDrop] = useState<OpenSeaDrop | null>(null);
  const [inspecting, setInspecting] = useState(false);
  const [name, setName] = useState("");
  const [wallets, setWallets] = useState<string[]>([]);
  const [quantity, setQuantity] = useState(1);
  const [rpcUrl, setRpcUrl] = useState(() => {
    const publicRPC = "https://rpc.mainnet.chain.robinhood.com";
    return window.localStorage.getItem("mintdesk-robinhood-rpc")
      || data.tasks.find((task) => task.network.chainId === 4663 && task.network.rpcUrl !== publicRPC)?.network.rpcUrl
      || publicRPC;
  });
  const [pollIntervalMilliseconds, setPollIntervalMilliseconds] = useState(500);
  const [maxGasPriceGwei, setMaxGasPriceGwei] = useState("1");
  const [maxTotalCostEth, setMaxTotalCostEth] = useState("");
  const [pendingDelete, setPendingDelete] = useState<CollectionTaskGroup | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [expandedCollections, setExpandedCollections] = useState<Set<string>>(() => new Set());
  const collections = useMemo(() => groupTasksByCollection(data.tasks), [data.tasks]);

  const inspect = async () => {
    setInspecting(true);
    try {
      const result = await bridge()!.InspectOpenSeaDrop(openSeaUrl);
      const normalized = {
        ...result,
        stages: Array.isArray(result.stages) ? result.stages : [],
        riskFlags: Array.isArray(result.riskFlags) ? result.riskFlags : [],
      };
      setDrop(normalized);
      setName(normalized.name);
      onNotice(`Inspected ${normalized.name} with ${normalized.stages.length} mint stage${normalized.stages.length === 1 ? "" : "s"}.`);
    } catch (error) {
      setDrop(null);
      onNotice(String(error));
    } finally {
      setInspecting(false);
    }
  };

  const toggleWallet = (wallet: string) => {
    setWallets((current) => current.includes(wallet) ? current.filter((item) => item !== wallet) : [...current, wallet]);
  };

  const create = async () => {
    if (!drop) { onNotice("Inspect an OpenSea URL first."); return; }
    try {
      window.localStorage.setItem("mintdesk-robinhood-rpc", rpcUrl.trim());
      const created = await bridge()!.CreateOpenSeaTasks({
        name,
        openSeaUrl: drop.url,
        walletAddresses: wallets,
        quantityPerWallet: quantity,
        rpcUrl,
        pollIntervalMilliseconds,
        maxGasPriceGwei,
        maxTotalCostEth,
      });
      onNotice(`Created a ${normalizedCollectionName(created)} collection group with ${created.length} wallet task${created.length === 1 ? "" : "s"}.`);
      await refresh();
    } catch (error) { onNotice(String(error)); }
  };

  const setCollectionEnabled = async (group: CollectionTaskGroup, enabled: boolean) => {
    try {
      await bridge()!.SetCollectionTasksEnabled(group.chainId, group.contract, enabled);
      onNotice(`${group.name} ${enabled ? "enabled" : "disabled"} for all wallets.`);
      await refresh();
    } catch (error) { onNotice(String(error)); }
  };

  const deleteCollection = async (group: CollectionTaskGroup) => {
    setDeleting(true);
    try {
      const deleted = await bridge()!.DeleteCollectionTasks(group.chainId, group.contract);
      onNotice(`Deleted ${deleted} wallet task${deleted === 1 ? "" : "s"} for ${group.name}.`);
      setPendingDelete(null);
      await refresh();
    } catch (error) {
      onNotice(`Delete failed: ${String(error)}`);
    } finally {
      setDeleting(false);
    }
  };

  const toggleCollectionExpanded = (key: string) => {
    setExpandedCollections((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  return <div className="view task-layout">
    <section className="task-panel existing-tasks collection-manager">
      <div className="section-title"><div><p className="eyebrow">SAVED COLLECTIONS</p><h2>{collections.length} collection{collections.length === 1 ? "" : "s"}</h2></div></div>
      {collections.length === 0 ? <EmptyState title="Create the first collection task" /> : <div className="collection-list">{collections.map((group) => {
        const enabledCount = group.tasks.filter((task) => task.enabled).length;
        const expanded = expandedCollections.has(group.key);
        return <article className="collection-card" key={group.key}>
          <div className="collection-card-head">
            <div className="collection-title"><strong>{group.name}</strong><span>{group.tasks.length} wallet{group.tasks.length === 1 ? "" : "s"} · {enabledCount} enabled</span><code>{shortAddress(group.contract)}</code></div>
            <div className="collection-actions">
              <button className="small-button" aria-expanded={expanded} onClick={() => toggleCollectionExpanded(group.key)}>{expanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}{expanded ? "Hide wallets" : `Show wallets (${group.tasks.length})`}</button>
              {enabledCount > 0
                ? <button className="small-button" disabled={data.running} title="Disable all wallet tasks" onClick={() => void setCollectionEnabled(group, false)}><PowerOff size={14} />Disable all</button>
                : <button className="small-button" disabled={data.running} title="Enable all wallet tasks" onClick={() => void setCollectionEnabled(group, true)}><Power size={14} />Enable all</button>}
              <button className="small-button destructive" disabled={data.running} title="Delete this collection and every wallet task" onClick={() => setPendingDelete(group)}><Trash2 size={14} />Delete</button>
            </div>
          </div>
          {expanded && <div className="collection-wallets">{group.tasks.map((saved) => <div className="task-row" key={saved.id}><TaskRow task={saved} showCollection={false} /></div>)}</div>}
        </article>;
      })}</div>}
      {data.running && <p className="subtle collection-lock-note">Stop the runner before enabling, disabling, or deleting a collection.</p>}
    </section>
    <section className="form-panel task-form">
      <div className="section-title"><div><p className="eyebrow">OPENSEA / ROBINHOOD</p><h2>Create a collection mint task</h2></div><Plus size={22} /></div>
      <p className="subtle task-help">Paste an OpenSea collection or mint URL. The contract, chain, stages, price, and wallet limits are inspected automatically and cannot be overridden manually.</p>
      <div className="inspect-row">
        <label className="field"><span>OpenSea mint URL</span><input value={openSeaUrl} onChange={(event) => setOpenSeaUrl(event.target.value)} placeholder="https://opensea.io/collection/..." /></label>
        <button className="primary-button" disabled={inspecting || !openSeaUrl.trim()} onClick={() => void inspect()}>{inspecting ? <LoaderCircle className="spin" size={17} /> : <RefreshCw size={17} />}Inspect OpenSea</button>
      </div>

      {drop && <>
        <div className="drop-summary">
          <div><p className="eyebrow">INSPECTED COLLECTION</p><h2>{drop.name}</h2><code>{drop.contract}</code></div>
          <div className="drop-facts"><span>Robinhood Chain · ID 4663</span><span>Supply {drop.totalSupply} / {drop.maxSupply}</span><span className={drop.verified ? "positive" : "warning"}>{drop.verified ? "OpenSea verified" : "Not verified by OpenSea"}</span></div>
        </div>
        {(drop.riskFlags ?? []).length > 0 && <div className="risk-box">{(drop.riskFlags ?? []).map((flag) => <span key={flag}>⚠ {flag}</span>)}</div>}

        <div className="stage-list">
          <div className="field-label">Automatic stage strategy</div>
          {(drop.stages ?? []).map((stage) => <div className={`stage-card ${stage.autoMintSupported ? "selected" : "blocked"}`} key={`${stage.stageIndex}-${stage.label}`}>
            <div className="strategy-check">{stage.autoMintSupported ? <CheckCircle2 size={16} /> : "—"}</div>
            <div className="stage-body"><div><strong>{stage.label || `Stage ${stage.stageIndex}`}</strong><span className={`stage-type ${stage.stageType === "PUBLIC_SALE" ? "public" : "allowlist"}`}>{stage.stageType === "PUBLIC_SALE" ? "Public" : "Allowlist"}</span></div><span>{formatStageTime(stage.startTime)} — {formatStageTime(stage.endTime)}</span><span>{stage.price} {stage.priceSymbol} · maximum {stage.maxTotalMintableByWallet} per wallet</span>{stage.blockReason && <em>{stage.blockReason}</em>}</div>
          </div>)}
          <p className="strategy-note">Each wallet uses every eligible stage in chronological order. Allowlist allocation is minted first; Public automatically fills only the remaining target.</p>
        </div>

        <div className="wallet-picker">
          <div className="field-label"><span>Select local wallets</span><button className="text-button" onClick={() => setWallets(wallets.length === data.wallets.length ? [] : [...data.wallets])}>{wallets.length === data.wallets.length ? "Clear selection" : "Select all"}</button></div>
          {data.wallets.length === 0 ? <p className="subtle">Import private keys or a recovery phrase in Wallets first.</p> : data.wallets.map((wallet) => <label className="wallet-choice" key={wallet}><input type="checkbox" checked={wallets.includes(wallet)} onChange={() => toggleWallet(wallet)} /><code>{wallet}</code></label>)}
        </div>

        <div className="form-grid compact-grid">
          <label className="field wide"><span>Collection task name</span><input value={name} onChange={(event) => setName(event.target.value)} /></label>
          <label className="field"><span>Mint quantity per wallet</span><input type="number" min="1" value={quantity} onChange={(event) => setQuantity(Number(event.target.value))} /></label>
          <label className="field"><span>Mint monitoring speed</span><select value={pollIntervalMilliseconds} onChange={(event) => setPollIntervalMilliseconds(Number(event.target.value))}>
            <option value={100}>Extreme · 100 ms</option>
            <option value={500}>Fast · 500 ms</option>
            <option value={2000}>Slow · 2 seconds</option>
            <option value={5000}>Very slow · 5 seconds</option>
          </select></label>
          <label className="field"><span>Maximum fee per gas (Gwei)</span><input value={maxGasPriceGwei} onChange={(event) => setMaxGasPriceGwei(event.target.value)} placeholder="1" /></label>
          <label className="field"><span>Maximum total cost per wallet (ETH)</span><input value={maxTotalCostEth} onChange={(event) => setMaxTotalCostEth(event.target.value)} placeholder="For example, 0.25" /></label>
          <label className="field wide"><span>Robinhood RPC</span><input value={rpcUrl} onChange={(event) => setRpcUrl(event.target.value)} /></label>
        </div>
        <p className="safety-note">Future tasks wait locally and connect one minute before the first stage. For each wallet, Mint Desk requests wallet-specific signed or Merkle allowlist calldata from OpenSea only while that stage is live, validates it against the inspected stage and SeaDrop contract, simulates it, waits for confirmation, and uses Public only for the remaining target.</p>
        <button className="primary-button full" disabled={wallets.length === 0 || !(drop.stages ?? []).some((stage) => stage.autoMintSupported)} onClick={() => void create()}><Plus size={17} />Create collection for {wallets.length} wallet{wallets.length === 1 ? "" : "s"}</button>
      </>}
    </section>
    {pendingDelete && <div className="dialog-backdrop" role="presentation" onMouseDown={() => { if (!deleting) setPendingDelete(null); }}>
      <section className="confirm-dialog" role="dialog" aria-modal="true" aria-labelledby="delete-dialog-title" onMouseDown={(event) => event.stopPropagation()}>
        <div className="confirm-dialog-icon"><Trash2 size={20} /></div>
        <div>
          <p className="eyebrow">CONFIRM DELETION</p>
          <h2 id="delete-dialog-title">Delete {pendingDelete.name}?</h2>
          <p>This permanently removes all {pendingDelete.tasks.length} wallet task{pendingDelete.tasks.length === 1 ? "" : "s"} in this collection. Encrypted wallets are not removed.</p>
        </div>
        <div className="confirm-dialog-actions">
          <button className="secondary-button" disabled={deleting} onClick={() => setPendingDelete(null)}>Cancel</button>
          <button className="danger-button dialog-danger" disabled={deleting} onClick={() => void deleteCollection(pendingDelete)}>{deleting ? <LoaderCircle className="spin" size={16} /> : <Trash2 size={16} />}{deleting ? "Deleting…" : `Delete ${pendingDelete.tasks.length} task${pendingDelete.tasks.length === 1 ? "" : "s"}`}</button>
        </div>
      </section>
    </div>}
  </div>;
}

function formatStageTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("en-US", { month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

type CollectionTaskGroup = {
  key: string;
  name: string;
  chainId: number;
  contract: string;
  tasks: Task[];
};

function groupTasksByCollection(tasks: Task[]): CollectionTaskGroup[] {
  const groups = new Map<string, CollectionTaskGroup>();
  for (const task of tasks) {
    const key = `${task.network.chainId}:${task.contract.toLowerCase()}`;
    let group = groups.get(key);
    if (!group) {
      const legacyWalletSuffix = /\s·\s0x[0-9a-f]{6,}…[0-9a-f]{4}$/i;
      group = {
        key,
        name: task.name.replace(legacyWalletSuffix, "") || task.openSea?.collection || task.name,
        chainId: task.network.chainId,
        contract: task.contract,
        tasks: [],
      };
      groups.set(key, group);
    }
    group.tasks.push(task);
  }
  return Array.from(groups.values());
}

function normalizedCollectionName(tasks: Task[]) {
  return tasks[0]?.name || tasks[0]?.openSea?.collection || "collection";
}

function Metric({ icon, label, value, action, onClick, tone = "" }: { icon: React.ReactNode; label: string; value: string; action?: string; onClick?: () => void; tone?: string }) {
  return <div className="metric"><div className={`metric-icon ${tone}`}>{icon}</div><div><p>{label}</p><strong>{value}</strong></div>{action && <button className="text-button" onClick={onClick}>{action}</button>}</div>;
}

function TaskRow({ task, showCollection = true }: { task: Task; showCollection?: boolean }) {
  const statusLabels: Record<string, string> = { ready: "Ready", scheduled: "Scheduled", watching: "Watching", broadcast: "Broadcast", failed: "Failed" };
  const stages = task.openSea?.stages?.length ? task.openSea.stages : task.openSea?.stage ? [task.openSea.stage] : [];
  const firstStage = [...stages].sort((left, right) => new Date(left.startTime).getTime() - new Date(right.startTime).getTime())[0];
  const watchTime = task.status === "scheduled" && firstStage?.startTime
    ? formatStageTime(new Date(new Date(firstStage.startTime).getTime() - 60_000).toISOString())
    : "";
  const title = showCollection ? (task.openSea?.collection || task.name) : shortAddress(task.walletAddress);
  const strategy = stages.length > 1 ? `${stages.length}-stage automatic strategy` : firstStage?.label || "legacy";
  const details = `${task.network.name} · target ${task.openSea?.targetQuantity || task.mint.quantity} · ${strategy}${showCollection ? ` · ${shortAddress(task.walletAddress)}` : ""}`;
  const status = task.enabled ? (statusLabels[task.status] || task.status) : "Disabled";
  return <div className="task-content"><div className={`status-dot ${task.enabled ? task.status : ""}`} /><div className="task-main"><strong>{title}</strong><span>{details}</span>{task.lastError && <em>{task.lastError}</em>}</div><div className="task-status"><span>{status}{watchTime ? ` · ${watchTime}` : ""}</span>{task.lastTxHash && <code>{shortAddress(task.lastTxHash)}</code>}</div></div>;
}

function EmptyState({ title, action, onClick }: { title: string; action?: string; onClick?: () => void }) {
  return <div className="empty"><Activity size={20} /><p>{title}</p>{action && <button className="text-button" onClick={onClick}>{action}</button>}</div>;
}
