import { getIconData, iconToSVG } from "@iconify/utils";
import { icons as f7Icons } from "@iconify-json/f7";
import { navIconNames } from "@liberty-resources/presentation/icons";

export type NavIconKey = keyof typeof navIconNames;

export const navIconSvg = Object.fromEntries(
  Object.entries(navIconNames).map(([key, name]) => {
    const iconData = getIconData(f7Icons, name);
    const iconSvg = iconData
      ? iconToSVG(iconData, {
          height: "1em",
          width: "1em",
        })
      : null;

    return [
      key,
      iconSvg
        ? `<svg viewBox="${iconSvg.attributes.viewBox}" width="1em" height="1em" aria-hidden="true">${iconSvg.body}</svg>`
        : "",
    ];
  }),
) as Record<NavIconKey, string>;
