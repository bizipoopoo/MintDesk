export namespace main {

	export class ChainConfig {
	    name: string;
	    chainId: number;
	    rpcUrl: string;

	    static createFrom(source: any = {}) {
	        return new ChainConfig(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.chainId = source["chainId"];
	        this.rpcUrl = source["rpcUrl"];
	    }
	}
	export class OpenSeaStage {
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

	    static createFrom(source: any = {}) {
	        return new OpenSeaStage(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.stageType = source["stageType"];
	        this.stageIndex = source["stageIndex"];
	        this.startTime = source["startTime"];
	        this.endTime = source["endTime"];
	        this.maxTotalMintableByWallet = source["maxTotalMintableByWallet"];
	        this.price = source["price"];
	        this.priceWei = source["priceWei"];
	        this.priceSymbol = source["priceSymbol"];
	        this.autoMintSupported = source["autoMintSupported"];
	        this.blockReason = source["blockReason"];
	    }
	}
	export class OpenSeaTaskSnapshot {
	    slug: string;
	    stage: OpenSeaStage;
	    collection: string;

	    static createFrom(source: any = {}) {
	        return new OpenSeaTaskSnapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.slug = source["slug"];
	        this.stage = this.convertValues(source["stage"], OpenSeaStage);
	        this.collection = source["collection"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MintConfig {
	    method: string;
	    quantity: number;
	    rawCalldata: string;
	    valueWei: string;
	    maxGasPriceWei: string;
	    maxTotalCostWei: string;

	    static createFrom(source: any = {}) {
	        return new MintConfig(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.quantity = source["quantity"];
	        this.rawCalldata = source["rawCalldata"];
	        this.valueWei = source["valueWei"];
	        this.maxGasPriceWei = source["maxGasPriceWei"];
	        this.maxTotalCostWei = source["maxTotalCostWei"];
	    }
	}
	export class SaleGate {
	    function: string;
	    expect: boolean;
	    pollIntervalSeconds: number;

	    static createFrom(source: any = {}) {
	        return new SaleGate(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.function = source["function"];
	        this.expect = source["expect"];
	        this.pollIntervalSeconds = source["pollIntervalSeconds"];
	    }
	}
	export class MintTask {
	    id: string;
	    name: string;
	    openSeaUrl: string;
	    walletAddress: string;
	    network: ChainConfig;
	    contract: string;
	    saleGate: SaleGate;
	    mint: MintConfig;
	    openSea?: OpenSeaTaskSnapshot;
	    enabled: boolean;
	    status: string;
	    lastCheckedAt?: string;
	    lastTxHash?: string;
	    lastError?: string;

	    static createFrom(source: any = {}) {
	        return new MintTask(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.openSeaUrl = source["openSeaUrl"];
	        this.walletAddress = source["walletAddress"];
	        this.network = this.convertValues(source["network"], ChainConfig);
	        this.contract = source["contract"];
	        this.saleGate = this.convertValues(source["saleGate"], SaleGate);
	        this.mint = this.convertValues(source["mint"], MintConfig);
	        this.openSea = this.convertValues(source["openSea"], OpenSeaTaskSnapshot);
	        this.enabled = source["enabled"];
	        this.status = source["status"];
	        this.lastCheckedAt = source["lastCheckedAt"];
	        this.lastTxHash = source["lastTxHash"];
	        this.lastError = source["lastError"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Dashboard {
	    dataDir: string;
	    wallets: string[];
	    tasks: MintTask[];
	    running: boolean;

	    static createFrom(source: any = {}) {
	        return new Dashboard(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataDir = source["dataDir"];
	        this.wallets = source["wallets"];
	        this.tasks = this.convertValues(source["tasks"], MintTask);
	        this.running = source["running"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}


	export class OpenSeaDrop {
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

	    static createFrom(source: any = {}) {
	        return new OpenSeaDrop(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.slug = source["slug"];
	        this.name = source["name"];
	        this.chain = source["chain"];
	        this.contract = source["contract"];
	        this.verified = source["verified"];
	        this.approved = source["approved"];
	        this.externalUrl = source["externalUrl"];
	        this.twitterUsername = source["twitterUsername"];
	        this.maxSupply = source["maxSupply"];
	        this.totalSupply = source["totalSupply"];
	        this.stages = this.convertValues(source["stages"], OpenSeaStage);
	        this.riskFlags = source["riskFlags"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class OpenSeaTaskInput {
	    name: string;
	    openSeaUrl: string;
	    walletAddresses: string[];
	    stageIndex: number;
	    quantityPerWallet: number;
	    rpcUrl: string;
	    pollIntervalSeconds: number;
	    maxGasPriceGwei: string;
	    maxTotalCostEth: string;

	    static createFrom(source: any = {}) {
	        return new OpenSeaTaskInput(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.openSeaUrl = source["openSeaUrl"];
	        this.walletAddresses = source["walletAddresses"];
	        this.stageIndex = source["stageIndex"];
	        this.quantityPerWallet = source["quantityPerWallet"];
	        this.rpcUrl = source["rpcUrl"];
	        this.pollIntervalSeconds = source["pollIntervalSeconds"];
	        this.maxGasPriceGwei = source["maxGasPriceGwei"];
	        this.maxTotalCostEth = source["maxTotalCostEth"];
	    }
	}
}
