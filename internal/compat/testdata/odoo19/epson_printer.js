// Contract fixture — excerpt of odoo/odoo, branch 19.0, commit
// a157617c921669c5c036509b341be6a07ad1069d.
// Source: addons/point_of_sale/static/src/app/utils/printer/epson_printer.js
// https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/utils/printer/epson_printer.js
// License: LGPL-3.0 (Odoo). Only the lines that define the printer URL are
// reproduced; refresh this excerpt when the pinned commit is updated.
    setup({ ip }) {
        super.setup(...arguments);

        const protocol = odoo.use_lna ? "http:" : window.location.protocol;
        this.url = protocol + "//" + ip;
        this.address = this.url + "/cgi-bin/epos/service.cgi?devid=local_printer";
