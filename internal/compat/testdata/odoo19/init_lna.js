// Contract fixture — excerpt of odoo/odoo, branch 19.0, commit
// a157617c921669c5c036509b341be6a07ad1069d.
// Source: addons/point_of_sale/static/src/app/utils/init_lna.js
// https://github.com/odoo/odoo/blob/a157617c921669c5c036509b341be6a07ad1069d/addons/point_of_sale/static/src/app/utils/init_lna.js
// License: LGPL-3.0 (Odoo). Only the lines that decide odoo.use_lna are
// reproduced; refresh this excerpt when the pinned commit is updated.
    try {
        const result = await navigator.permissions.query({ name: "local-network-access" });
        processLNAState(result);
        result.onchange = () => processLNAState(result);
    } catch {
        odoo.use_lna = false;
