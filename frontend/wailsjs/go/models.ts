export namespace models {
	
	export class AppConfig {
	    AutoDownloadCover: boolean;
	    MaxDownloadConcurrency: number;
	    MaxConvertConcurrency: number;
	
	    static createFrom(source: any = {}) {
	        return new AppConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.AutoDownloadCover = source["AutoDownloadCover"];
	        this.MaxDownloadConcurrency = source["MaxDownloadConcurrency"];
	        this.MaxConvertConcurrency = source["MaxConvertConcurrency"];
	    }
	}
	export class FileInfo {
	    ID: number;
	    Path: string;
	    SongName: string;
	    Format: string;
	    Size: number;
	    CoverStatus: string;
	    ConvertStatus: string;
	    Logs: string[];
	
	    static createFrom(source: any = {}) {
	        return new FileInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ID = source["ID"];
	        this.Path = source["Path"];
	        this.SongName = source["SongName"];
	        this.Format = source["Format"];
	        this.Size = source["Size"];
	        this.CoverStatus = source["CoverStatus"];
	        this.ConvertStatus = source["ConvertStatus"];
	        this.Logs = source["Logs"];
	    }
	}

}

