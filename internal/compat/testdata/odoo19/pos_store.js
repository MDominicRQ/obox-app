// Contract fixture — excerpts of odoo/odoo, branch 19.0, commit
// a157617c921669c5c036509b341be6a07ad1069d.
// Source: addons/point_of_sale/static/src/app/services/pos_store.js
// https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/services/pos_store.js
// License: LGPL-3.0 (Odoo). Only the lines that establish the call order
// between printer creation and initLNA() are reproduced; refresh this excerpt
// when the pinned commit is updated.

    async setup(env, { ... }) {
        await this.initServerData();

        // ... proxy connection, tab handling and debug components ...

        initLNA(this.notification, (type, message) => {
            this.lnaState = { type, message };
        });
    }

    async initServerData() {
        await this.processServerData();

    async processServerData() {
        // Create printer with hardware proxy, this will override related model data
        this.unwatched.printers = [];
        for (const relPrinter of this.models["pos.printer"].getAll()) {
            const printer = relPrinter.raw;
            const HWPrinter = this.createPrinter(printer);

            HWPrinter.config = printer;
            this.unwatched.printers.push(HWPrinter);
        }

    async afterProcessServerData() {
        if (this.config.other_devices && this.config.epson_printer_ip) {
            this.hardwareProxy.printer = new EpsonPrinter({ ip: this.config.epson_printer_ip });
        }

    createPrinter(config) {
        if (config.printer_type === "epson_epos") {
            return new EpsonPrinter({ ip: config.epson_printer_ip });
        }
