use crate::local_db::LocalResult;
use xml::reader::{EventReader, ParserConfig, XmlEvent};
use xmltree::{Element, XMLNode};

pub fn parse_document_xml(source: &str) -> Result<Element, xmltree::ParseError> {
    let reader = EventReader::new_with_config(
        source.as_bytes(),
        ParserConfig::new().ignore_comments(false),
    );
    let mut stack: Vec<Element> = Vec::new();
    let mut root = None;

    for event in reader {
        let node = match event.map_err(xmltree::ParseError::MalformedXml)? {
            XmlEvent::StartElement {
                name,
                attributes,
                namespace,
            } => {
                stack.push(Element {
                    prefix: name.prefix,
                    namespace: name.namespace,
                    namespaces: Some(namespace),
                    name: name.local_name,
                    attributes: attributes
                        .into_iter()
                        .map(|attribute| (attribute.name.borrow().to_repr(), attribute.value))
                        .collect(),
                    children: Vec::new(),
                });
                continue;
            }
            XmlEvent::EndElement { .. } => {
                let element = stack.pop().ok_or(xmltree::ParseError::CannotParse)?;
                if stack.is_empty() {
                    root = Some(element);
                    continue;
                }
                XMLNode::Element(element)
            }
            XmlEvent::Characters(value) => XMLNode::Text(value),
            XmlEvent::Whitespace(value)
                if stack.last().is_some_and(|element| element.name == "t") =>
            {
                XMLNode::Text(value)
            }
            XmlEvent::CData(value) => XMLNode::CData(value),
            XmlEvent::Comment(value) => XMLNode::Comment(value),
            XmlEvent::ProcessingInstruction { name, data } => {
                XMLNode::ProcessingInstruction(name, data)
            }
            XmlEvent::StartDocument { .. } | XmlEvent::Whitespace(_) => continue,
            XmlEvent::EndDocument => break,
        };
        if let Some(parent) = stack.last_mut() {
            parent.children.push(node);
        }
    }

    root.ok_or(xmltree::ParseError::CannotParse)
}

pub fn set_cell_value(row: &mut Element, index: usize, value: &str) -> LocalResult<()> {
    let mut cells = child_elements_mut(row, "tc");
    let cell = cells
        .get_mut(index)
        .ok_or_else(|| format!("会议纪要模板缺少第 {index} 个单元格。"))?;
    set_first_text(cell, value);
    Ok(())
}

pub fn set_first_text(element: &mut Element, value: &str) {
    if replace_first_text(element, value) {
        return;
    }

    let mut text = Element::new("w:t");
    text.children.push(XMLNode::Text(value.to_string()));
    let mut run = Element::new("w:r");
    run.children.push(XMLNode::Element(text));
    let mut paragraph = Element::new("w:p");
    paragraph.children.push(XMLNode::Element(run));
    element.children.push(XMLNode::Element(paragraph));
}

pub fn clone_with_text(template: &Element, value: &str) -> Element {
    let mut cloned = template.clone();
    set_first_text(&mut cloned, value);
    cloned
}

pub fn child_elements<'a>(element: &'a Element, name: &str) -> Vec<&'a Element> {
    element
        .children
        .iter()
        .filter_map(|child| match child {
            XMLNode::Element(child_element) if local_name(&child_element.name) == name => {
                Some(child_element)
            }
            _ => None,
        })
        .collect()
}

pub fn child_elements_mut<'a>(element: &'a mut Element, name: &str) -> Vec<&'a mut Element> {
    element
        .children
        .iter_mut()
        .filter_map(|child| match child {
            XMLNode::Element(child_element) if local_name(&child_element.name) == name => {
                Some(child_element)
            }
            _ => None,
        })
        .collect()
}

pub fn child_elements_count(element: &Element, name: &str) -> usize {
    child_elements(element, name).len()
}

pub fn remove_last_child_element(element: &mut Element, name: &str) {
    if let Some(index) = element.children.iter().rposition(|child| {
        matches!(child, XMLNode::Element(child_element) if local_name(&child_element.name) == name)
    }) {
        element.children.remove(index);
    }
}

pub fn find_child_mut<'a>(element: &'a mut Element, name: &str) -> Option<&'a mut Element> {
    element.children.iter_mut().find_map(|child| match child {
        XMLNode::Element(child_element) if local_name(&child_element.name) == name => {
            Some(child_element)
        }
        _ => None,
    })
}

pub fn local_name(name: &str) -> &str {
    name.rsplit(':').next().unwrap_or(name)
}

fn replace_first_text(element: &mut Element, value: &str) -> bool {
    for child in &mut element.children {
        match child {
            XMLNode::Element(child_element) => {
                if local_name(&child_element.name) == "t" {
                    if let Some(XMLNode::Text(text)) = child_element
                        .children
                        .iter_mut()
                        .find(|node| matches!(node, XMLNode::Text(_)))
                    {
                        *text = value.to_string();
                        return true;
                    }
                }

                if replace_first_text(child_element, value) {
                    return true;
                }
            }
            XMLNode::Text(text) => {
                *text = value.to_string();
                return true;
            }
            _ => {}
        }
    }

    false
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn preserves_word_text_whitespace_and_escaped_mixed_content_without_indentation() {
        let source = r#"<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:p>
    <w:r>
      <w:t xml:space="preserve">   </w:t>
      <w:t> </w:t>
      <w:t xml:space="preserve"> 混合 &amp; &lt;文本&gt; <![CDATA[<原样&>]]> 尾部 </w:t>
    </w:r>
  </w:p>
</w:document>"#;
        let parsed = parse_document_xml(source).unwrap();
        assert_eq!(parsed.children.len(), 1);
        let paragraph = child_elements(&parsed, "p")[0];
        assert_eq!(paragraph.children.len(), 1);
        let run = child_elements(paragraph, "r")[0];
        assert_eq!(run.children.len(), 3);
        let text = child_elements(run, "t");
        let actual: Vec<_> = text
            .iter()
            .map(|element| element.get_text().unwrap_or_default().into_owned())
            .collect();
        assert_eq!(actual, ["   ", " ", " 混合 & <文本> <原样&> 尾部 "]);

        let mut output = Vec::new();
        parsed.write(&mut output).unwrap();
        let round_trip = parse_document_xml(std::str::from_utf8(&output).unwrap()).unwrap();
        assert_eq!(round_trip, parsed);
    }
}
