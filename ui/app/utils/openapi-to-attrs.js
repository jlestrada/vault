import { attr } from '@ember-data/model';
import { assign } from '@ember/polyfills';
import { camelize, capitalize } from '@ember/string';

export const expandOpenApiProps = function (props) {
  let attrs = {};
  // expand all attributes
  for (const propName in props) {
    const prop = props[propName];
    let { description, items, type, format, isId, deprecated } = prop;
    if (deprecated === true) {
      continue;
    }
    let { name, value, group, sensitive, editType } = prop['x-vault-displayAttrs'] || {};

    if (type === 'integer') {
      type = 'number';
    }

    editType = editType || type;

    if (format === 'seconds') {
      editType = 'ttl';
    } else if (items) {
      editType = items.type + capitalize(type);
    }

    let attrDefn = {
      editType,
      helpText: description,
      possibleValues: prop['enum'],
      fieldValue: isId ? 'id' : null,
      fieldGroup: group || 'default',
      readOnly: isId,
      defaultValue: value || null,
    };

    if (type === 'object' && !!value) {
      attrDefn.defaultValue = () => {
        return value;
      };
    }

    if (sensitive) {
      attrDefn.sensitive = true;
    }

    //only set a label if we have one from OpenAPI
    //otherwise the propName will be humanized by the form-field component
    if (name) {
      attrDefn.label = name;
    }

    // ttls write as a string and read as a number
    // so setting type on them runs the wrong transform
    if (editType !== 'ttl' && type !== 'array') {
      attrDefn.type = type;
    }

    // loop to remove empty vals
    for (let attrProp in attrDefn) {
      if (attrDefn[attrProp] == null) {
        delete attrDefn[attrProp];
      }
    }
    attrs[camelize(propName)] = attrDefn;
  }
  return attrs;
};

export const combineAttributes = function (oldAttrs, newProps) {
  let newAttrs = {};
  let newFields = [];
  if (oldAttrs) {
    oldAttrs.forEach(function (value, name) {
      if (newProps[name]) {
        newAttrs[name] = attr(newProps[name].type, assign({}, newProps[name], value.options));
      } else {
        newAttrs[name] = attr(value.type, value.options);
      }
    });
  }
  for (let prop in newProps) {
    if (newAttrs[prop]) {
      continue;
    } else {
      newAttrs[prop] = attr(newProps[prop].type, newProps[prop]);
      newFields.push(prop);
    }
  }
  return { attrs: newAttrs, newFields };
};

export const combineFields = function (currentFields, newFields, excludedFields) {
  let otherFields = newFields.filter((field) => {
    return !currentFields.includes(field) && !excludedFields.includes(field);
  });
  if (otherFields.length) {
    currentFields = currentFields.concat(otherFields);
  }
  return currentFields;
};

export const combineFieldGroups = function (currentGroups, newFields, excludedFields) {
  let allFields = [];
  for (let group of currentGroups) {
    let fieldName = Object.keys(group)[0];
    allFields = allFields.concat(group[fieldName]);
  }
  let otherFields = newFields.filter((field) => {
    return !allFields.includes(field) && !excludedFields.includes(field);
  });
  if (otherFields.length) {
    currentGroups[0].default = currentGroups[0].default.concat(otherFields);
  }

  return currentGroups;
};
