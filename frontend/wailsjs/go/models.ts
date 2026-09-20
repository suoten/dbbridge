export namespace history {
	
	export class MigrationRecord {
	    id: number;
	    startTime: string;
	    endTime: string;
	    duration: string;
	    sourceType: string;
	    sourceHost: string;
	    sourceDB: string;
	    targetType: string;
	    targetHost: string;
	    targetDB: string;
	    tablesTotal: number;
	    tablesSuccess: number;
	    tablesFailed: number;
	    totalRows: number;
	    status: string;
	    tableDetails?: number[];
	    backups?: number[];
	    createdAt: string;
	
	    static createFrom(source: any = {}) {
	        return new MigrationRecord(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.startTime = source["startTime"];
	        this.endTime = source["endTime"];
	        this.duration = source["duration"];
	        this.sourceType = source["sourceType"];
	        this.sourceHost = source["sourceHost"];
	        this.sourceDB = source["sourceDB"];
	        this.targetType = source["targetType"];
	        this.targetHost = source["targetHost"];
	        this.targetDB = source["targetDB"];
	        this.tablesTotal = source["tablesTotal"];
	        this.tablesSuccess = source["tablesSuccess"];
	        this.tablesFailed = source["tablesFailed"];
	        this.totalRows = source["totalRows"];
	        this.status = source["status"];
	        this.tableDetails = source["tableDetails"];
	        this.backups = source["backups"];
	        this.createdAt = source["createdAt"];
	    }
	}

}

export namespace main {
	
	export class ConnectionRequest {
	    type: string;
	    host: string;
	    port: number;
	    username: string;
	    password: string;
	    database: string;
	    sslMode: string;
	    charset: string;
	    instance: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.database = source["database"];
	        this.sslMode = source["sslMode"];
	        this.charset = source["charset"];
	        this.instance = source["instance"];
	    }
	}
	export class CheckCompatibilityRequest {
	    source: ConnectionRequest;
	    target: ConnectionRequest;
	    tables?: string[];
	
	    static createFrom(source: any = {}) {
	        return new CheckCompatibilityRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = this.convertValues(source["source"], ConnectionRequest);
	        this.target = this.convertValues(source["target"], ConnectionRequest);
	        this.tables = source["tables"];
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
	
	export class GetBackupTablesResult {
	    success: boolean;
	    tables?: service.BackupTableItem[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GetBackupTablesResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.tables = this.convertValues(source["tables"], service.BackupTableItem);
	        this.error = source["error"];
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
	export class GetTableSchemaResult {
	    success: boolean;
	    schema?: types.TableSchema;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GetTableSchemaResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.schema = this.convertValues(source["schema"], types.TableSchema);
	        this.error = source["error"];
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
	export class GetTablesResult {
	    success: boolean;
	    tables?: types.TableMeta[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GetTablesResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.tables = this.convertValues(source["tables"], types.TableMeta);
	        this.error = source["error"];
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
	export class LintSQLRequest {
	    sourceDialect: string;
	    targetDialect: string;
	    sql: string;
	
	    static createFrom(source: any = {}) {
	        return new LintSQLRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceDialect = source["sourceDialect"];
	        this.targetDialect = source["targetDialect"];
	        this.sql = source["sql"];
	    }
	}
	export class RestoreAllResult {
	    successCount: number;
	    failedCount: number;
	    failedItems?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RestoreAllResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.successCount = source["successCount"];
	        this.failedCount = source["failedCount"];
	        this.failedItems = source["failedItems"];
	    }
	}
	export class RestoreRequest {
	    connection: ConnectionRequest;
	    backupName?: string;
	    backupNames?: string[];
	
	    static createFrom(source: any = {}) {
	        return new RestoreRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connection = this.convertValues(source["connection"], ConnectionRequest);
	        this.backupName = source["backupName"];
	        this.backupNames = source["backupNames"];
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
	export class RestoreTableResult {
	    success: boolean;
	    backupName: string;
	    original?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new RestoreTableResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.backupName = source["backupName"];
	        this.original = source["original"];
	        this.error = source["error"];
	    }
	}
	export class SimpleResult {
	    success: boolean;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new SimpleResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.error = source["error"];
	    }
	}
	export class StartMigrationRequest {
	    source: ConnectionRequest;
	    target: ConnectionRequest;
	    tables?: string[];
	    structureOnly: boolean;
	    dataOnly: boolean;
	    batchSize: number;
	    concurrency: number;
	    dropIfExists: boolean;
	    ignoreErrors: boolean;
	    backupBefore: boolean;
	    autoRollback: boolean;
	    migrateTriggers: boolean;
	    migrateRoutines: boolean;
	    schemaDefault?: string;
	    schemaTables?: Record<string, string>;
	    tablespace?: string;
	
	    static createFrom(source: any = {}) {
	        return new StartMigrationRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = this.convertValues(source["source"], ConnectionRequest);
	        this.target = this.convertValues(source["target"], ConnectionRequest);
	        this.tables = source["tables"];
	        this.structureOnly = source["structureOnly"];
	        this.dataOnly = source["dataOnly"];
	        this.batchSize = source["batchSize"];
	        this.concurrency = source["concurrency"];
	        this.dropIfExists = source["dropIfExists"];
	        this.ignoreErrors = source["ignoreErrors"];
	        this.backupBefore = source["backupBefore"];
	        this.autoRollback = source["autoRollback"];
	        this.migrateTriggers = source["migrateTriggers"];
	        this.migrateRoutines = source["migrateRoutines"];
	        this.schemaDefault = source["schemaDefault"];
	        this.schemaTables = source["schemaTables"];
	        this.tablespace = source["tablespace"];
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
	export class TestConnectionResult {
	    success: boolean;
	    version: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new TestConnectionResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.version = source["version"];
	        this.error = source["error"];
	    }
	}

}

export namespace service {
	
	export class BackupTableItem {
	    backupName: string;
	    originalName: string;
	
	    static createFrom(source: any = {}) {
	        return new BackupTableItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.backupName = source["backupName"];
	        this.originalName = source["originalName"];
	    }
	}
	export class CompatItem {
	    category: string;
	    severity: string;
	    message: string;
	    suggestion?: string;
	
	    static createFrom(source: any = {}) {
	        return new CompatItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.category = source["category"];
	        this.severity = source["severity"];
	        this.message = source["message"];
	        this.suggestion = source["suggestion"];
	    }
	}
	export class CompatTableReport {
	    table: string;
	    status: string;
	    items?: CompatItem[];
	
	    static createFrom(source: any = {}) {
	        return new CompatTableReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.table = source["table"];
	        this.status = source["status"];
	        this.items = this.convertValues(source["items"], CompatItem);
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
	export class CompatReport {
	    success: boolean;
	    source: string;
	    target: string;
	    tables: CompatTableReport[];
	    summary?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new CompatReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.source = source["source"];
	        this.target = source["target"];
	        this.tables = this.convertValues(source["tables"], CompatTableReport);
	        this.summary = source["summary"];
	        this.error = source["error"];
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
	
	export class ConnStrResult {
	    success: boolean;
	    dbType: string;
	    templates?: Record<string, string>;
	    notes?: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnStrResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.dbType = source["dbType"];
	        this.templates = source["templates"];
	        this.notes = source["notes"];
	        this.error = source["error"];
	    }
	}
	export class ConvertSQLRequest {
	    sourceDialect: string;
	    targetDialect: string;
	    sql: string;
	
	    static createFrom(source: any = {}) {
	        return new ConvertSQLRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceDialect = source["sourceDialect"];
	        this.targetDialect = source["targetDialect"];
	        this.sql = source["sql"];
	    }
	}
	export class ConvertSQLResult {
	    success: boolean;
	    converted?: string;
	    warnings?: string[];
	    changes?: string[];
	    error?: string;
	    totalStatements: number;
	    convertedCount: number;
	    passedThrough: number;
	    skippedCount: number;
	
	    static createFrom(source: any = {}) {
	        return new ConvertSQLResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.converted = source["converted"];
	        this.warnings = source["warnings"];
	        this.changes = source["changes"];
	        this.error = source["error"];
	        this.totalStatements = source["totalStatements"];
	        this.convertedCount = source["convertedCount"];
	        this.passedThrough = source["passedThrough"];
	        this.skippedCount = source["skippedCount"];
	    }
	}
	export class GuideItem {
	    category: string;
	    severity: string;
	    table?: string;
	    object?: string;
	    message: string;
	    suggestion?: string;
	
	    static createFrom(source: any = {}) {
	        return new GuideItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.category = source["category"];
	        this.severity = source["severity"];
	        this.table = source["table"];
	        this.object = source["object"];
	        this.message = source["message"];
	        this.suggestion = source["suggestion"];
	    }
	}
	export class GuideTypeChange {
	    table: string;
	    column: string;
	    sourceType: string;
	    targetType: string;
	    note?: string;
	
	    static createFrom(source: any = {}) {
	        return new GuideTypeChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.table = source["table"];
	        this.column = source["column"];
	        this.sourceType = source["sourceType"];
	        this.targetType = source["targetType"];
	        this.note = source["note"];
	    }
	}
	export class GuideReport {
	    success: boolean;
	    source: string;
	    target: string;
	    tables: number;
	    changes?: GuideTypeChange[];
	    items?: GuideItem[];
	    triggersTotal: number;
	    triggersOk: number;
	    triggersFailed: number;
	    routinesTotal: number;
	    routinesOk: number;
	    routinesFailed: number;
	    summary?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new GuideReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.source = source["source"];
	        this.target = source["target"];
	        this.tables = source["tables"];
	        this.changes = this.convertValues(source["changes"], GuideTypeChange);
	        this.items = this.convertValues(source["items"], GuideItem);
	        this.triggersTotal = source["triggersTotal"];
	        this.triggersOk = source["triggersOk"];
	        this.triggersFailed = source["triggersFailed"];
	        this.routinesTotal = source["routinesTotal"];
	        this.routinesOk = source["routinesOk"];
	        this.routinesFailed = source["routinesFailed"];
	        this.summary = source["summary"];
	        this.error = source["error"];
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
	
	export class TableValidation {
	    table: string;
	    status: string;
	    sourceRows: number;
	    targetRows: number;
	    sampled: number;
	    compared: number;
	    missingRows?: string[];
	    fieldMismatch?: string[];
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new TableValidation(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.table = source["table"];
	        this.status = source["status"];
	        this.sourceRows = source["sourceRows"];
	        this.targetRows = source["targetRows"];
	        this.sampled = source["sampled"];
	        this.compared = source["compared"];
	        this.missingRows = source["missingRows"];
	        this.fieldMismatch = source["fieldMismatch"];
	        this.error = source["error"];
	    }
	}
	export class ValidateReport {
	    success: boolean;
	    matchCount: number;
	    mismatchCount: number;
	    errorCount: number;
	    tables: TableValidation[];
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new ValidateReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.success = source["success"];
	        this.matchCount = source["matchCount"];
	        this.mismatchCount = source["mismatchCount"];
	        this.errorCount = source["errorCount"];
	        this.tables = this.convertValues(source["tables"], TableValidation);
	        this.summary = source["summary"];
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
	export class ValidateRequest {
	    source: types.ConnectionConfig;
	    target: types.ConnectionConfig;
	    tables?: string[];
	    sampleSize?: number;
	
	    static createFrom(source: any = {}) {
	        return new ValidateRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.source = this.convertValues(source["source"], types.ConnectionConfig);
	        this.target = this.convertValues(source["target"], types.ConnectionConfig);
	        this.tables = source["tables"];
	        this.sampleSize = source["sampleSize"];
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

}

export namespace sqllint {
	
	export class Finding {
	    line: number;
	    column: number;
	    severity: string;
	    category: string;
	    message: string;
	    suggestion: string;
	    snippet: string;
	
	    static createFrom(source: any = {}) {
	        return new Finding(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.line = source["line"];
	        this.column = source["column"];
	        this.severity = source["severity"];
	        this.category = source["category"];
	        this.message = source["message"];
	        this.suggestion = source["suggestion"];
	        this.snippet = source["snippet"];
	    }
	}
	export class Report {
	    sourceDialect: string;
	    targetDialect: string;
	    findings: Finding[];
	    errors: number;
	    warnings: number;
	    infos: number;
	    totalLines: number;
	
	    static createFrom(source: any = {}) {
	        return new Report(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceDialect = source["sourceDialect"];
	        this.targetDialect = source["targetDialect"];
	        this.findings = this.convertValues(source["findings"], Finding);
	        this.errors = source["errors"];
	        this.warnings = source["warnings"];
	        this.infos = source["infos"];
	        this.totalLines = source["totalLines"];
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

}

export namespace types {
	
	export class BackupInfo {
	    originalTable: string;
	    backupTable: string;
	    createdAt: string;
	    restored?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new BackupInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.originalTable = source["originalTable"];
	        this.backupTable = source["backupTable"];
	        this.createdAt = source["createdAt"];
	        this.restored = source["restored"];
	    }
	}
	export class CheckMeta {
	    name: string;
	    definition: string;
	
	    static createFrom(source: any = {}) {
	        return new CheckMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.definition = source["definition"];
	    }
	}
	export class ColumnMeta {
	    name: string;
	    dataType: string;
	    baseType: string;
	    length?: number;
	    precision?: number;
	    scale?: number;
	    nullable: boolean;
	    defaultValue?: string;
	    autoIncrement: boolean;
	    unsigned?: boolean;
	    comment?: string;
	    isPrimaryKey: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ColumnMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.dataType = source["dataType"];
	        this.baseType = source["baseType"];
	        this.length = source["length"];
	        this.precision = source["precision"];
	        this.scale = source["scale"];
	        this.nullable = source["nullable"];
	        this.defaultValue = source["defaultValue"];
	        this.autoIncrement = source["autoIncrement"];
	        this.unsigned = source["unsigned"];
	        this.comment = source["comment"];
	        this.isPrimaryKey = source["isPrimaryKey"];
	    }
	}
	export class ConnectionConfig {
	    type: string;
	    host: string;
	    port: number;
	    username: string;
	    password: string;
	    database: string;
	    sslMode?: string;
	    charset?: string;
	    instance?: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.database = source["database"];
	        this.sslMode = source["sslMode"];
	        this.charset = source["charset"];
	        this.instance = source["instance"];
	    }
	}
	export class ForeignKeyMeta {
	    name: string;
	    columns: string[];
	    refTable: string;
	    refColumns: string[];
	    onDelete?: string;
	    onUpdate?: string;
	
	    static createFrom(source: any = {}) {
	        return new ForeignKeyMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.columns = source["columns"];
	        this.refTable = source["refTable"];
	        this.refColumns = source["refColumns"];
	        this.onDelete = source["onDelete"];
	        this.onUpdate = source["onUpdate"];
	    }
	}
	export class IndexMeta {
	    name: string;
	    columns: string[];
	    isUnique: boolean;
	    isPrimary: boolean;
	    type?: string;
	
	    static createFrom(source: any = {}) {
	        return new IndexMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.columns = source["columns"];
	        this.isUnique = source["isUnique"];
	        this.isPrimary = source["isPrimary"];
	        this.type = source["type"];
	    }
	}
	export class TableReport {
	    tableName: string;
	    rows: number;
	    status: string;
	    error?: string;
	    duration: string;
	    backupTable?: string;
	    rolledBack?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TableReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tableName = source["tableName"];
	        this.rows = source["rows"];
	        this.status = source["status"];
	        this.error = source["error"];
	        this.duration = source["duration"];
	        this.backupTable = source["backupTable"];
	        this.rolledBack = source["rolledBack"];
	    }
	}
	export class MigrationReport {
	    startTime: string;
	    endTime: string;
	    duration: string;
	    logFile?: string;
	    tablesTotal: number;
	    tablesSuccess: number;
	    tablesFailed: number;
	    tablesCancelled?: number;
	    totalRows: number;
	    failedTables?: string[];
	    tableDetails: TableReport[];
	    backups?: BackupInfo[];
	    rollbackCount?: number;
	    error?: string;
	    triggersMigrated?: number;
	    routinesMigrated?: number;
	    triggerErrors?: string[];
	    routineErrors?: string[];
	    fkErrors?: string[];
	
	    static createFrom(source: any = {}) {
	        return new MigrationReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.startTime = source["startTime"];
	        this.endTime = source["endTime"];
	        this.duration = source["duration"];
	        this.logFile = source["logFile"];
	        this.tablesTotal = source["tablesTotal"];
	        this.tablesSuccess = source["tablesSuccess"];
	        this.tablesFailed = source["tablesFailed"];
	        this.tablesCancelled = source["tablesCancelled"];
	        this.totalRows = source["totalRows"];
	        this.failedTables = source["failedTables"];
	        this.tableDetails = this.convertValues(source["tableDetails"], TableReport);
	        this.backups = this.convertValues(source["backups"], BackupInfo);
	        this.rollbackCount = source["rollbackCount"];
	        this.error = source["error"];
	        this.triggersMigrated = source["triggersMigrated"];
	        this.routinesMigrated = source["routinesMigrated"];
	        this.triggerErrors = source["triggerErrors"];
	        this.routineErrors = source["routineErrors"];
	        this.fkErrors = source["fkErrors"];
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
	export class TableMeta {
	    name: string;
	    comment?: string;
	
	    static createFrom(source: any = {}) {
	        return new TableMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.comment = source["comment"];
	    }
	}
	
	export class TriggerMeta {
	    name: string;
	    event: string;
	    timing: string;
	    table: string;
	    body: string;
	    forEachRow: boolean;
	    columns?: string[];
	
	    static createFrom(source: any = {}) {
	        return new TriggerMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.event = source["event"];
	        this.timing = source["timing"];
	        this.table = source["table"];
	        this.body = source["body"];
	        this.forEachRow = source["forEachRow"];
	        this.columns = source["columns"];
	    }
	}
	export class TableSchema {
	    name: string;
	    comment?: string;
	    columns: ColumnMeta[];
	    indexes?: IndexMeta[];
	    foreignKeys?: ForeignKeyMeta[];
	    engine?: string;
	    charset?: string;
	    collation?: string;
	    checks?: CheckMeta[];
	    triggers?: TriggerMeta[];
	
	    static createFrom(source: any = {}) {
	        return new TableSchema(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.comment = source["comment"];
	        this.columns = this.convertValues(source["columns"], ColumnMeta);
	        this.indexes = this.convertValues(source["indexes"], IndexMeta);
	        this.foreignKeys = this.convertValues(source["foreignKeys"], ForeignKeyMeta);
	        this.engine = source["engine"];
	        this.charset = source["charset"];
	        this.collation = source["collation"];
	        this.checks = this.convertValues(source["checks"], CheckMeta);
	        this.triggers = this.convertValues(source["triggers"], TriggerMeta);
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

}

