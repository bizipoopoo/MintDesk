import { useEffect, useMemo, useState } from "react";
import {
  Activity,
  CheckCircle2,
  CircleStop,
  Copy,
  KeyRound,
  LayoutDashboard,
  LoaderCircle,
  LockKeyhole,
  Play,
  Plus,
  RefreshCw,
  ShieldCheck,
  WalletCards,
} from "lucide-react";

type Task = {
  id: string;
  name: string;
  openSeaUrl: string;
  walletAddress: string;
  network: { name: string; chainId: number; rpcUrl: string };
  contract: string;
  saleGate: { function: string; expect: boolean; pollIntervalSeconds: number };
  mint: { method: string; quantity: number; rawCalldata: string; valueWei: string; maxGasPriceWei: string; maxTotalCostWei: string };
  openSea?: { slug: string; collection: string; stage: OpenSeaStage };
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
  stageIndex: number;
  quantityPerWallet: number;
  rpcUrl: string;
  pollIntervalSeconds: number;
  maxGasPriceGwei: string;
  maxTotalCostEth: string;
};

type Dashboard = { dataDir: string; wallets: string[]; tasks: Task[]; running: boolean };
type Page = "dashboard" | "wallets" | "tasks";

declare global {
  interface Window {
    go?: {
      main?: {
        App: {
          Dashboard: () => Promise<Dashboard>;
          ImportPrivateKey: (privateKey: string, password: string) => Promise<string>;
          ImportMnemonic: (mnemonic: string, password: string, count: number) => Promise<string[]>;
          InspectOpenSeaDrop: (url: string) => Promise<OpenSeaDrop>;
          CreateOpenSeaTasks: (input: OpenSeaTaskInput) => Promise<Task[]>;
          SetTaskEnabled: (id: string, enabled: boolean) => Promise<void>;
          ArmAndRun: (password: string, confirmed: boolean) => Promise<void>;
          StopRunner: () => Promise<void>;
        };
      };
    };
  }
}

const shortAddress = (address: string) => `${address.slice(0, 7)}...${address.slice(-5)}`;
const bridge = () => window.go?.main?.App;

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
        <div className="brand"><div className="brand-mark"><Activity size={19} /></div><span>Mint Desk</span></div>
        <nav>
          <NavButton active={page === "dashboard"} icon={<LayoutDashboard size={18} />} label="Overview" onClick={() => setPage("dashboard")} />
          <NavButton active={page === "wallets"} icon={<WalletCards size={18} />} label="Wallets" onClick={() => setPage("wallets")} />
          <NavButton active={page === "tasks"} icon={<CheckCircle2 size={18} />} label="Mint tasks" onClick={() => setPage("tasks")} />
        </nav>
        <div className="sidebar-foot"><ShieldCheck size={17} /><span>Local encrypted store</span></div>
      </aside>

      <main className="main">
        <header className="topbar">
          <div>
            <p className="eyebrow">LOCAL MINT OPERATIONS</p>
            <h1>{page === "dashboard" ? "Command center" : page === "wallets" ? "Wallet vault" : "Mint tasks"}</h1>
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
      </main>
    </div>
  );
}

function NavButton({ active, icon, label, onClick }: { active: boolean; icon: React.ReactNode; label: string; onClick: () => void }) {
  return <button className={`nav-button ${active ? "active" : ""}`} onClick={onClick}>{icon}<span>{label}</span></button>;
}

function Overview({ data, activeTasks, onNavigate, onNotice, refresh }: { data: Dashboard; activeTasks: number; onNavigate: (page: Page) => void; onNotice: (message: string) => void; refresh: () => Promise<void> }) {
  const [password, setPassword] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const run = async () => {
    try {
      await bridge()!.ArmAndRun(password, confirmed);
      setPassword("");
      onNotice("Runner 已启动。远期任务会定时等待，并在 Mint 前 1 分钟开始监听。");
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
      {data.tasks.length === 0 ? <EmptyState title="No mint tasks yet" action="Create mint task" onClick={() => onNavigate("tasks")} /> : <div className="task-list">{data.tasks.slice(0, 5).map((task) => <TaskRow key={task.id} task={task} />)}</div>}
    </section>
  </div>;
}

function WalletPage({ data, refresh, onNotice }: { data: Dashboard; refresh: () => Promise<void>; onNotice: (message: string) => void }) {
  const [mode, setMode] = useState<"key" | "mnemonic">("key");
  const [secret, setSecret] = useState("");
  const [password, setPassword] = useState("");
  const [count, setCount] = useState(3);
  const importWallet = async () => {
    try {
      const result = mode === "key" ? [await bridge()!.ImportPrivateKey(secret, password)] : await bridge()!.ImportMnemonic(secret, password, count);
      setSecret(""); setPassword(""); onNotice(`Imported ${result.length} encrypted wallet${result.length === 1 ? "" : "s"}.`); await refresh();
    } catch (error) { onNotice(String(error)); }
  };
  return <div className="view two-column">
    <section className="form-panel">
      <div className="section-title"><div><p className="eyebrow">IMPORT</p><h2>Local wallet vault</h2></div><KeyRound size={22} /></div>
      <div className="segmented"><button className={mode === "key" ? "selected" : ""} onClick={() => setMode("key")}>Private key</button><button className={mode === "mnemonic" ? "selected" : ""} onClick={() => setMode("mnemonic")}>Recovery phrase</button></div>
      <label className="field"><span>{mode === "key" ? "Private key" : "Recovery phrase"}</span><textarea rows={mode === "key" ? 3 : 5} value={secret} onChange={(event) => setSecret(event.target.value)} spellCheck={false} placeholder={mode === "key" ? "0x..." : "word word word ..."} /></label>
      {mode === "mnemonic" && <label className="field"><span>Addresses to derive</span><input type="number" min="1" max="20" value={count} onChange={(event) => setCount(Number(event.target.value))} /></label>}
      <label className="field"><span>New keystore password</span><input type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Encrypts this local import" /></label>
      <button className="primary-button full" onClick={() => void importWallet()}><LockKeyhole size={17} />Import encrypted wallet</button>
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
  const [stageIndex, setStageIndex] = useState<number | null>(null);
  const [wallets, setWallets] = useState<string[]>([]);
  const [quantity, setQuantity] = useState(1);
  const [rpcUrl, setRpcUrl] = useState("https://rpc.mainnet.chain.robinhood.com");
  const [pollInterval, setPollInterval] = useState(5);
  const [maxGasPriceGwei, setMaxGasPriceGwei] = useState("1");
  const [maxTotalCostEth, setMaxTotalCostEth] = useState("");

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
      const supported = normalized.stages.find((stage) => stage.autoMintSupported);
      setStageIndex(supported?.stageIndex ?? null);
      onNotice(`已从 OpenSea 解析 ${normalized.name}，共 ${normalized.stages.length} 个阶段。`);
    } catch (error) {
      setDrop(null);
      setStageIndex(null);
      onNotice(String(error));
    } finally {
      setInspecting(false);
    }
  };

  const toggleWallet = (wallet: string) => {
    setWallets((current) => current.includes(wallet) ? current.filter((item) => item !== wallet) : [...current, wallet]);
  };

  const create = async () => {
    if (!drop || stageIndex === null) { onNotice("请先解析 OpenSea 链接并选择可执行的公售阶段。"); return; }
    try {
      const created = await bridge()!.CreateOpenSeaTasks({
        name,
        openSeaUrl: drop.url,
        walletAddresses: wallets,
        stageIndex,
        quantityPerWallet: quantity,
        rpcUrl,
        pollIntervalSeconds: pollInterval,
        maxGasPriceGwei,
        maxTotalCostEth,
      });
      onNotice(`已创建 ${created.length} 个钱包任务；不同钱包并发，同钱包 nonce 串行。`);
      await refresh();
    } catch (error) { onNotice(String(error)); }
  };
  const toggle = async (current: Task) => { try { await bridge()!.SetTaskEnabled(current.id, !current.enabled); await refresh(); } catch (error) { onNotice(String(error)); } };
  return <div className="view task-layout">
    <section className="task-panel existing-tasks">
      <div className="section-title"><div><p className="eyebrow">SAVED TASKS</p><h2>Mint queue</h2></div></div>
      {data.tasks.length === 0 ? <EmptyState title="Create the first task" /> : <div className="task-list">{data.tasks.map((saved) => <div className="task-row" key={saved.id}><TaskRow task={saved} /><label className="switch"><input type="checkbox" checked={saved.enabled} onChange={() => void toggle(saved)} /><span /></label></div>)}</div>}
    </section>
    <section className="form-panel task-form">
      <div className="section-title"><div><p className="eyebrow">OPENSEA / ROBINHOOD</p><h2>从 OpenSea 创建 mint 任务</h2></div><Plus size={22} /></div>
      <p className="subtle task-help">只需粘贴 OpenSea collection/mint 链接。合约、链、阶段、价格与钱包限额均自动解析，不允许手工覆盖。</p>
      <div className="inspect-row">
        <label className="field"><span>OpenSea mint 链接</span><input value={openSeaUrl} onChange={(event) => setOpenSeaUrl(event.target.value)} placeholder="https://opensea.io/collection/..." /></label>
        <button className="primary-button" disabled={inspecting || !openSeaUrl.trim()} onClick={() => void inspect()}>{inspecting ? <LoaderCircle className="spin" size={17} /> : <RefreshCw size={17} />}解析 OpenSea</button>
      </div>

      {drop && <>
        <div className="drop-summary">
          <div><p className="eyebrow">已解析集合</p><h2>{drop.name}</h2><code>{drop.contract}</code></div>
          <div className="drop-facts"><span>Robinhood Chain · ID 4663</span><span>Supply {drop.totalSupply} / {drop.maxSupply}</span><span className={drop.verified ? "positive" : "warning"}>{drop.verified ? "OpenSea verified" : "OpenSea 未验证"}</span></div>
        </div>
        {(drop.riskFlags ?? []).length > 0 && <div className="risk-box">{(drop.riskFlags ?? []).map((flag) => <span key={flag}>⚠ {flag}</span>)}</div>}

        <div className="stage-list">
          <div className="field-label">选择 OpenSea mint 阶段</div>
          {(drop.stages ?? []).map((stage) => <label className={`stage-card ${stageIndex === stage.stageIndex ? "selected" : ""} ${!stage.autoMintSupported ? "blocked" : ""}`} key={`${stage.stageIndex}-${stage.label}`}>
            <input type="radio" name="stage" disabled={!stage.autoMintSupported} checked={stageIndex === stage.stageIndex} onChange={() => setStageIndex(stage.stageIndex)} />
            <div className="stage-body"><div><strong>{stage.label || `Stage ${stage.stageIndex}`}</strong><span className={`stage-type ${stage.autoMintSupported ? "public" : "allowlist"}`}>{stage.stageType === "PUBLIC_SALE" ? "公售" : "白名单"}</span></div><span>{formatStageTime(stage.startTime)} — {formatStageTime(stage.endTime)}</span><span>{stage.price} {stage.priceSymbol} · 每钱包最多 {stage.maxTotalMintableByWallet}</span>{stage.blockReason && <em>{stage.blockReason}</em>}</div>
          </label>)}
        </div>

        <div className="wallet-picker">
          <div className="field-label"><span>选择多个本地钱包</span><button className="text-button" onClick={() => setWallets(wallets.length === data.wallets.length ? [] : [...data.wallets])}>{wallets.length === data.wallets.length ? "取消全选" : "全选"}</button></div>
          {data.wallets.length === 0 ? <p className="subtle">请先到 Wallets 导入私钥或助记词。</p> : data.wallets.map((wallet) => <label className="wallet-choice" key={wallet}><input type="checkbox" checked={wallets.includes(wallet)} onChange={() => toggleWallet(wallet)} /><code>{wallet}</code></label>)}
        </div>

        <div className="form-grid compact-grid">
          <label className="field wide"><span>任务名称</span><input value={name} onChange={(event) => setName(event.target.value)} /></label>
          <label className="field"><span>每钱包 mint 数量</span><input type="number" min="1" value={quantity} onChange={(event) => setQuantity(Number(event.target.value))} /></label>
          <label className="field"><span>轮询间隔（秒）</span><input type="number" min="2" value={pollInterval} onChange={(event) => setPollInterval(Number(event.target.value))} /></label>
          <label className="field"><span>最大 gas price（Gwei）</span><input value={maxGasPriceGwei} onChange={(event) => setMaxGasPriceGwei(event.target.value)} placeholder="1" /></label>
          <label className="field"><span>单钱包最大总成本（ETH）</span><input value={maxTotalCostEth} onChange={(event) => setMaxTotalCostEth(event.target.value)} placeholder="例如 0.25" /></label>
          <label className="field wide"><span>Robinhood RPC</span><input value={rpcUrl} onChange={(event) => setRpcUrl(event.target.value)} /></label>
        </div>
        <p className="safety-note">远期任务使用系统定时器等待，Mint 前 1 分钟才连接 RPC 并开始轮询；任务广播、失败或阶段结束后会立即停止监听。广播前会再次读取链上 SeaDrop 价格、时间、钱包已 mint 数量和供应量，并执行交易模拟。白名单阶段会展示，但在没有 OpenSea 钱包专属签名/proof 时不会创建自动交易。</p>
        <button className="primary-button full" disabled={stageIndex === null || wallets.length === 0} onClick={() => void create()}><Plus size={17} />为 {wallets.length} 个钱包创建并发任务</button>
      </>}
    </section>
  </div>;
}

function formatStageTime(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

function Metric({ icon, label, value, action, onClick, tone = "" }: { icon: React.ReactNode; label: string; value: string; action?: string; onClick?: () => void; tone?: string }) {
  return <div className="metric"><div className={`metric-icon ${tone}`}>{icon}</div><div><p>{label}</p><strong>{value}</strong></div>{action && <button className="text-button" onClick={onClick}>{action}</button>}</div>;
}

function TaskRow({ task }: { task: Task }) {
  const statusLabels: Record<string, string> = { ready: "未启动", scheduled: "定时等待", watching: "监听中", broadcast: "已广播", failed: "失败" };
  const watchTime = task.status === "scheduled" && task.openSea?.stage.startTime
    ? formatStageTime(new Date(new Date(task.openSea.stage.startTime).getTime() - 60_000).toISOString())
    : "";
  return <div className="task-content"><div className={`status-dot ${task.status}`} /><div className="task-main"><strong>{task.name}</strong><span>{task.network.name} · 每钱包 {task.mint.quantity} · {task.openSea?.stage.label || "legacy"} · {shortAddress(task.walletAddress)}</span>{task.lastError && <em>{task.lastError}</em>}</div><div className="task-status"><span>{statusLabels[task.status] || task.status}{watchTime ? ` · ${watchTime}` : ""}</span>{task.lastTxHash && <code>{shortAddress(task.lastTxHash)}</code>}</div></div>;
}

function EmptyState({ title, action, onClick }: { title: string; action?: string; onClick?: () => void }) {
  return <div className="empty"><Activity size={20} /><p>{title}</p>{action && <button className="text-button" onClick={onClick}>{action}</button>}</div>;
}
